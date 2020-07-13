package context

import (
	"fmt"
	"net"

	"free5gc/lib/openapi/Nnrf_NFDiscovery"
	"free5gc/lib/openapi/Nnrf_NFManagement"
	"free5gc/lib/openapi/Nudm_SubscriberDataManagement"
	"free5gc/src/smf/factory"
	"free5gc/src/smf/logger"

	"free5gc/lib/openapi/models"
	"free5gc/lib/pfcp/pfcpType"
	"free5gc/lib/pfcp/pfcpUdp"

	"github.com/google/uuid"
)

func init() {
	smfContext.NfInstanceID = uuid.New().String()
}

var smfContext SMFContext

type SMFContext struct {
	Name         string
	NfInstanceID string

	URIScheme   models.UriScheme
	HTTPAddress string
	HTTPPort    int
	CPNodeID    pfcpType.NodeID

	UDMProfile models.NfProfile

	SnssaiInfos []models.SnssaiSmfInfoItem

	UPNodeIDs []pfcpType.NodeID
	Key       string
	PEM       string
	KeyLog    string

	UESubNet      *net.IPNet
	UEAddressTemp net.IP

	NrfUri                         string
	NFManagementClient             *Nnrf_NFManagement.APIClient
	NFDiscoveryClient              *Nnrf_NFDiscovery.APIClient
	SubscriberDataManagementClient *Nudm_SubscriberDataManagement.APIClient
	DNNInfo                        map[string]factory.DNNInfo

	UserPlaneInformation UserPlaneInformation
	OnlySupportIPv4      bool
	OnlySupportIPv6      bool
	//*** For ULCL ** //
	ULCLSupport         bool
	UERoutingPaths      map[string][]factory.Path
	UEPreConfigPathPool map[string]*UEPreConfigPaths
	LocalSEIDCount      uint64
}

func AllocUEIP() net.IP {
	smfContext.UEAddressTemp[3]++
	return smfContext.UEAddressTemp
}

func AllocateLocalSEID() uint64 {
	smfContext.LocalSEIDCount++
	return smfContext.LocalSEIDCount
}

func InitSmfContext(config *factory.Config) {
	if config == nil {
		logger.CtxLog.Infof("Config is nil")
	}

	logger.CtxLog.Infof("smfconfig Info: Version[%s] Description[%s]", config.Info.Version, config.Info.Description)
	configuration := config.Configuration
	if configuration.SmfName != "" {
		smfContext.Name = configuration.SmfName
	}

	sbi := configuration.Sbi
	smfContext.URIScheme = models.UriScheme(sbi.Scheme)
	smfContext.HTTPAddress = "127.0.0.1" // default localhost
	smfContext.HTTPPort = 29502          // default port
	if sbi != nil {
		if sbi.IPv4Addr != "" {
			smfContext.HTTPAddress = sbi.IPv4Addr
		}
		if sbi.Port != 0 {
			smfContext.HTTPPort = sbi.Port
		}

		if tls := sbi.TLS; tls != nil {
			smfContext.Key = tls.Key
			smfContext.PEM = tls.PEM
		}
	}
	if configuration.NrfUri != "" {
		smfContext.NrfUri = configuration.NrfUri
	} else {
		smfContext.NrfUri = fmt.Sprintf("%s://%s:%d", smfContext.URIScheme, smfContext.HTTPAddress, 29510)
	}

	if pfcp := configuration.PFCP; pfcp != nil {
		if pfcp.Port == 0 {
			pfcp.Port = pfcpUdp.PFCP_PORT
		}
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", pfcp.Addr, pfcp.Port))
		if err != nil {
			logger.CtxLog.Warnf("PFCP Parse Addr Fail: %v", err)
		}

		smfContext.CPNodeID.NodeIdType = 0
		smfContext.CPNodeID.NodeIdValue = addr.IP.To4()
	}

	_, ipNet, err := net.ParseCIDR(configuration.UESubnet)
	if err != nil {
		logger.InitLog.Errorln(err)
	}

	smfContext.DNNInfo = configuration.DNN
	smfContext.UESubNet = ipNet
	smfContext.UEAddressTemp = ipNet.IP

	// Set client and set url
	ManagementConfig := Nnrf_NFManagement.NewConfiguration()
	ManagementConfig.SetBasePath(SMF_Self().NrfUri)
	smfContext.NFManagementClient = Nnrf_NFManagement.NewAPIClient(ManagementConfig)

	NFDiscovryConfig := Nnrf_NFDiscovery.NewConfiguration()
	NFDiscovryConfig.SetBasePath(SMF_Self().NrfUri)
	smfContext.NFDiscoveryClient = Nnrf_NFDiscovery.NewAPIClient(NFDiscovryConfig)

	smfContext.ULCLSupport = configuration.ULCL

	smfContext.SnssaiInfos = configuration.SNssaiInfo

	smfContext.OnlySupportIPv4 = true

	processUPTopology(&configuration.UserPlaneInformation)

	SetupNFProfile(config)
}

func InitSMFUERouting(routingConfig *factory.RoutingConfig) {

	if !smfContext.ULCLSupport {
		return
	}

	if routingConfig == nil {
		logger.CtxLog.Infof("Routing Config is nil")
	}

	logger.CtxLog.Infof("ue routing config Info: Version[%s] Description[%s]",
		routingConfig.Info.Version, routingConfig.Info.Description)

	UERoutingInfo := routingConfig.UERoutingInfo
	ueRoutingPaths := make(map[string][]factory.Path)
	smfContext.UEPreConfigPathPool = make(map[string]*UEPreConfigPaths)

	for _, routingInfo := range UERoutingInfo {
		supi := routingInfo.SUPI

		ueRoutingPaths[supi] = routingInfo.PathList
	}

	for supi, paths := range ueRoutingPaths {
		uePreConfigPaths, err := NewUEPreConfigPaths(supi, paths)
		if err != nil {
			logger.CtxLog.Warnln(err)
			continue
		}

		smfContext.UEPreConfigPathPool[supi] = uePreConfigPaths
	}

}

func SMF_Self() *SMFContext {
	return &smfContext
}

func GetUserPlaneInformation() *UserPlaneInformation {
	return &smfContext.UserPlaneInformation
}
