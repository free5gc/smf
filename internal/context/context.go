package context

import (
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/oauth"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

func Init() {
	smfContext.NfInstanceID = uuid.New().String()
}

type NFContext interface {
	AuthorizationCheck(token string, serviceName models.ServiceName) error
}

var _ NFContext = &SMFContext{}

var smfContext SMFContext

type SMFContext struct {
	Name         string
	NfInstanceID string

	URIScheme    models.UriScheme
	BindingIPv4  string
	RegisterIPv4 string
	SBIPort      int

	// N4 interface-related
	CPNodeID     pfcpType.NodeID
	ExternalAddr string
	ListenAddr   string

	UDMProfile models.NfProfile
	NfProfile  NFProfile

	Key    string
	PEM    string
	KeyLog string

	SnssaiInfos []*SnssaiSmfInfo

	NrfUri                 string
	NrfCertPem             string
	Locality               string
	AssocFailAlertInterval time.Duration
	AssocFailRetryInterval time.Duration
	OAuth2Required         bool

	UserPlaneInformation  *UserPlaneInformation
	Ctx                   context.Context
	PFCPCancelFunc        context.CancelFunc
	PfcpHeartbeatInterval time.Duration

	PfcpHeartbeatRetries   int
	PfcpHeartbeatTolerance int
	PfcpHeartbeatTimeout   time.Duration

	SmContextPool    sync.Map
	CanonicalRef     sync.Map
	SeidSMContextMap sync.Map

	// Now only "IPv4" supported
	// TODO: support "IPv6", "IPv4v6", "Ethernet"
	SupportedPDUSessionType string

	// *** For ULCL *** //
	ULCLSupport         bool
	ULCLGroups          map[string][]string
	UEPreConfigPathPool map[string]*UEPreConfigPaths
	UEDefaultPathPool   map[string]*UEDefaultPaths
	LocalSEIDCount      uint64

	// Each pdu session should have a unique charging id
	ChargingIDGenerator *idgenerator.IDGenerator
}

/*
func (smfContext *SMFContext) ProcEachSMContext(procFunc func(*SMContext) bool) {
	smfContext.SmContextPool.Range(func(key, value interface{}) bool {
		smContext := value.(*SMContext)
		return procFunc(smContext) // processing function determines if loop continues
	})
}*/

func canonicalName(id string, pduSessID int32) string {
	return fmt.Sprintf("%s-%d", id, pduSessID)
}

func (smfContext *SMFContext) ResolveRef(id string, pduSessID int32) (string, error) {
	if value, ok := smfContext.CanonicalRef.Load(canonicalName(id, pduSessID)); ok {
		ref := value.(string)
		return ref, nil
	} else {
		return "", fmt.Errorf("UE[%s] - PDUSessionID[%d] not found in SMFContext", id, pduSessID)
	}
}

// *** add unit test ***//
func (smfContext *SMFContext) GetSMContextByRef(ref string) *SMContext {
	// TODO: neu schreiben, ProcEachSMContext nutzen
	var smCtx *SMContext
	if value, ok := smfContext.SmContextPool.Load(ref); ok {
		smCtx = value.(*SMContext)
	}
	return smCtx
}

func (smfContext *SMFContext) GetSMContextById(id string, pduSessID int32) *SMContext {
	// TODO: neu schreiben, ProcEachSMContext nutzen
	var smCtx *SMContext
	ref, err := smfContext.ResolveRef(id, pduSessID)
	if err != nil {
		return nil
	}
	if value, ok := smfContext.SmContextPool.Load(ref); ok {
		smCtx = value.(*SMContext)
	}
	return smCtx
}

// *** add unit test ***//
func (smfContext *SMFContext) RemoveSMContext(smContext *SMContext) {
	logger.CtxLog.Traceln("In RemoveSMContext")

	for _, dataPath := range smContext.Tunnel.DataPathPool {
		// TODO: free PDR IDs?
		dataPath.DeactivateTunnelAndPDR(smContext)
	}

	// free UE IP
	if smContext.SelectedUPF != nil && smContext.PDUAddress != nil {
		logger.PduSessLog.Infof("UE[%s] PDUSessionID[%d] Release IP[%s]",
			smContext.Supi, smContext.PDUSessionID, smContext.PDUAddress.String())
		GetUserPlaneInformation().
			ReleaseUEIP(smContext.SelectedUPF, smContext.PDUAddress, smContext.UseStaticIP)
		smContext.SelectedUPF = nil
	}

	// TODO: what about PFCP session rules?

	// TODO: still required or done elsewhere?
	for _, pfcpSessionContext := range smContext.PFCPSessionContexts {
		smfContext.SeidSMContextMap.Delete(pfcpSessionContext.LocalSEID)
	}

	ReleaseTEID(smContext.LocalULTeid)
	ReleaseTEID(smContext.LocalDLTeid)

	smfContext.SmContextPool.Delete(smContext.Ref)
	smfContext.CanonicalRef.Delete(canonicalName(smContext.Supi, smContext.PDUSessionID))
	smContext.Log.Infof("smContext[%s] is deleted from pool", smContext.Ref)
}

// *** add unit test ***//
func (smfContext *SMFContext) GetSMContextBySEID(seid uint64) *SMContext {
	if value, ok := smfContext.SeidSMContextMap.Load(seid); ok {
		smContext := value.(*SMContext)
		return smContext
	}
	return nil
}

func GenerateChargingID() int32 {
	if smfContext.ChargingIDGenerator != nil {
		if id, err := smfContext.ChargingIDGenerator.Allocate(); err == nil {
			return int32(id)
		}
	}
	return 0
}

func ResolveIP(host string) net.IP {
	if addr, err := net.ResolveIPAddr("ip", host); err != nil {
		return nil
	} else {
		return addr.IP
	}
}

func (s *SMFContext) ExternalIP() net.IP {
	return ResolveIP(s.ExternalAddr)
}

func (s *SMFContext) ListenIP() net.IP {
	return ResolveIP(s.ListenAddr)
}

// RetrieveDnnInformation gets the corresponding dnn info from S-NSSAI and DNN
func RetrieveDnnInformation(snssai *models.Snssai, dnn string) *SnssaiSmfDnnInfo {
	for _, snssaiInfo := range GetSelf().SnssaiInfos {
		if snssaiInfo.Snssai.EqualModelsSnssai(snssai) {
			return snssaiInfo.DnnInfos[dnn]
		}
	}
	return nil
}

func (s *SMFContext) AllocateLocalSEID() uint64 {
	return atomic.AddUint64(&s.LocalSEIDCount, 1)
}

func InitSmfContext(config *factory.Config) {
	if config == nil {
		logger.CtxLog.Error("Config is nil")
		return
	}

	logger.CtxLog.Infof("smfconfig Info: Version[%s] Description[%s]", config.Info.Version, config.Info.Description)
	configuration := config.Configuration
	if configuration.SmfName != "" {
		smfContext.Name = configuration.SmfName
	}

	sbi := configuration.Sbi
	if sbi == nil {
		logger.CtxLog.Errorln("Configuration needs \"sbi\" value")
		return
	} else {
		smfContext.URIScheme = models.UriScheme(sbi.Scheme)
		smfContext.RegisterIPv4 = factory.SmfSbiDefaultIPv4 // default localhost
		smfContext.SBIPort = factory.SmfSbiDefaultPort      // default port
		if sbi.RegisterIPv4 != "" {
			smfContext.RegisterIPv4 = sbi.RegisterIPv4
		}
		if sbi.Port != 0 {
			smfContext.SBIPort = sbi.Port
		}

		if tls := sbi.Tls; tls != nil {
			smfContext.Key = tls.Key
			smfContext.PEM = tls.Pem
		}

		smfContext.BindingIPv4 = os.Getenv(sbi.BindingIPv4)
		if smfContext.BindingIPv4 != "" {
			logger.CtxLog.Info("Parsing ServerIPv4 address from ENV Variable.")
		} else {
			smfContext.BindingIPv4 = sbi.BindingIPv4
			if smfContext.BindingIPv4 == "" {
				logger.CtxLog.Warn("Error parsing ServerIPv4 address as string. Using the 0.0.0.0 address as default.")
				smfContext.BindingIPv4 = "0.0.0.0"
			}
		}
	}

	if configuration.NrfUri != "" {
		smfContext.NrfUri = configuration.NrfUri
	} else {
		logger.CtxLog.Warn("NRF Uri is empty! Using localhost as NRF IPv4 address.")
		smfContext.NrfUri = fmt.Sprintf("%s://%s:%d", smfContext.URIScheme, "127.0.0.1", 29510)
	}
	smfContext.NrfCertPem = configuration.NrfCertPem

	if pfcp := configuration.PFCP; pfcp != nil {
		smfContext.ListenAddr = pfcp.ListenAddr
		smfContext.ExternalAddr = pfcp.ExternalAddr

		if ip := net.ParseIP(pfcp.NodeID); ip == nil {
			smfContext.CPNodeID = pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeFqdn,
				FQDN:       pfcp.NodeID,
			}
		} else {
			ipv4 := ip.To4()
			if ipv4 != nil {
				smfContext.CPNodeID = pfcpType.NodeID{
					NodeIdType: pfcpType.NodeIdTypeIpv4Address,
					IP:         ipv4,
				}
			} else {
				smfContext.CPNodeID = pfcpType.NodeID{
					NodeIdType: pfcpType.NodeIdTypeIpv6Address,
					IP:         ip,
				}
			}
		}

		smfContext.PfcpHeartbeatInterval = pfcp.HeartbeatInterval
		var multipleOfInterval time.Duration = 5
		if pfcp.AssocFailAlertInterval == 0 {
			smfContext.AssocFailAlertInterval = multipleOfInterval * time.Minute
		} else {
			smfContext.AssocFailAlertInterval = pfcp.AssocFailAlertInterval
		}
		if pfcp.AssocFailRetryInterval == 0 {
			smfContext.AssocFailRetryInterval = multipleOfInterval * time.Second
		} else {
			smfContext.AssocFailRetryInterval = pfcp.AssocFailRetryInterval
		}
	}

	smfContext.SnssaiInfos = make([]*SnssaiSmfInfo, 0, len(configuration.SNssaiInfo))

	for _, snssaiInfoConfig := range configuration.SNssaiInfo {
		snssaiInfo := SnssaiSmfInfo{}
		snssaiInfo.Snssai = SNssai{
			Sst: snssaiInfoConfig.SNssai.Sst,
			Sd:  snssaiInfoConfig.SNssai.Sd,
		}

		snssaiInfo.DnnInfos = make(map[string]*SnssaiSmfDnnInfo)

		for _, dnnInfoConfig := range snssaiInfoConfig.DnnInfos {
			dnnInfo := SnssaiSmfDnnInfo{}
			if dnnInfoConfig.DNS != nil {
				dnnInfo.DNS.IPv4Addr = net.ParseIP(dnnInfoConfig.DNS.IPv4Addr).To4()
				dnnInfo.DNS.IPv6Addr = net.ParseIP(dnnInfoConfig.DNS.IPv6Addr).To16()
			}
			if dnnInfoConfig.PCSCF != nil {
				dnnInfo.PCSCF.IPv4Addr = net.ParseIP(dnnInfoConfig.PCSCF.IPv4Addr).To4()
			}
			snssaiInfo.DnnInfos[dnnInfoConfig.Dnn] = &dnnInfo
		}
		smfContext.SnssaiInfos = append(smfContext.SnssaiInfos, &snssaiInfo)
	}

	smfContext.ULCLSupport = configuration.ULCL

	smfContext.SupportedPDUSessionType = "IPv4"

	smfContext.UserPlaneInformation = NewUserPlaneInformation(&configuration.UserPlaneInformation)

	smfContext.ChargingIDGenerator = idgenerator.NewGenerator(1, math.MaxUint32)

	smfContext.SetupNFProfile(config)

	smfContext.Locality = configuration.Locality

	TeidGenerator = idgenerator.NewGenerator(1, math.MaxUint32)
}

func InitSMFUERouting(routingConfig *factory.RoutingConfig) {
	if !smfContext.ULCLSupport {
		return
	}

	if routingConfig == nil {
		logger.CtxLog.Error("configuration needs the routing config")
		return
	}

	logger.CtxLog.Infof("ue routing config Info: Version[%s] Description[%s]",
		routingConfig.Info.Version, routingConfig.Info.Description)

	UERoutingInfo := routingConfig.UERoutingInfo
	smfContext.UEPreConfigPathPool = make(map[string]*UEPreConfigPaths)
	smfContext.UEDefaultPathPool = make(map[string]*UEDefaultPaths)
	smfContext.ULCLGroups = make(map[string][]string)

	for groupName, routingInfo := range UERoutingInfo {
		logger.CtxLog.Debugln("Set context for ULCL group: ", groupName)
		smfContext.ULCLGroups[groupName] = routingInfo.Members
		uePreConfigPaths, err := NewUEPreConfigPaths(routingInfo.SpecificPaths)
		if err != nil {
			logger.CtxLog.Warnln(err)
		} else {
			smfContext.UEPreConfigPathPool[groupName] = uePreConfigPaths
		}
		ueDefaultPaths, err := NewUEDefaultPaths(smfContext.UserPlaneInformation, routingInfo.Topology)
		if err != nil {
			logger.CtxLog.Warnln(err)
		} else {
			smfContext.UEDefaultPathPool[groupName] = ueDefaultPaths
		}
	}
}

func GetSelf() *SMFContext {
	return &smfContext
}

func GetUserPlaneInformation() *UserPlaneInformation {
	return smfContext.UserPlaneInformation
}

func GetUEDefaultPathPool(groupName string) *UEDefaultPaths {
	return smfContext.UEDefaultPathPool[groupName]
}

func (c *SMFContext) GetTokenCtx(serviceName models.ServiceName, targetNF models.NfType) (
	context.Context, *models.ProblemDetails, error,
) {
	if !c.OAuth2Required {
		return context.TODO(), nil, nil
	}
	return oauth.GetTokenCtx(models.NfType_SMF, targetNF,
		c.NfInstanceID, c.NrfUri, string(serviceName))
}

func (c *SMFContext) AuthorizationCheck(token string, serviceName models.ServiceName) error {
	if !c.OAuth2Required {
		return nil
	}
	return oauth.VerifyOAuth(token, string(serviceName), c.NrfCertPem)
}
