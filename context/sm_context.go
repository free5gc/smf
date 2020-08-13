package context

import (
	"context"
	"encoding/json"
	"fmt"
	"free5gc/lib/nas/nasConvert"
	"free5gc/lib/nas/nasMessage"
	"free5gc/lib/openapi"
	"free5gc/lib/openapi/Namf_Communication"
	"free5gc/lib/openapi/Nnrf_NFDiscovery"
	"free5gc/lib/openapi/Npcf_SMPolicyControl"
	"free5gc/lib/openapi/models"
	"free5gc/lib/pfcp/pfcpType"
	"free5gc/src/smf/logger"
	"net"
	"net/http"

	"github.com/google/uuid"
)

var smContextPool map[string]*SMContext
var canonicalRef map[string]string
var seidSMContextMap map[uint64]*SMContext

var smContextCount uint64

type SMState int

const (
	PDUSessionInactive SMState = 0
	PDUSessionActive   SMState = 1
)

func init() {
	smContextPool = make(map[string]*SMContext)
	seidSMContextMap = make(map[uint64]*SMContext)
	canonicalRef = make(map[string]string)
}

func GetSMContextCount() uint64 {
	smContextCount++
	return smContextCount
}

type SMContext struct {
	Ref string

	LocalSEID  uint64
	RemoteSEID uint64

	UnauthenticatedSupi bool
	// SUPI or PEI
	Supi           string
	Pei            string
	Pti            uint8
	Identifier     string
	Gpsi           string
	PDUSessionID   int32
	Dnn            string
	Snssai         *models.Snssai
	HplmnSnssai    *models.Snssai
	ServingNetwork *models.PlmnId
	ServingNfId    string

	UpCnxState models.UpCnxState

	AnType          models.AccessType
	RatType         models.RatType
	PresenceInLadn  models.PresenceState
	UeLocation      *models.UserLocation
	UeTimeZone      string
	AddUeLocation   *models.UserLocation
	OldPduSessionId int32
	HoState         models.HoState

	PDUAddress             net.IP
	SelectedPDUSessionType uint8

	DnnConfiguration models.DnnConfiguration

	// Client
	SMPolicyClient      *Npcf_SMPolicyControl.APIClient
	CommunicationClient *Namf_Communication.APIClient

	AMFProfile         models.NfProfile
	SelectedPCFProfile models.NfProfile
	SmStatusNotifyUri  string

	SMState SMState

	Tunnel    *UPTunnel
	BPManager *BPManager
	//NodeID(string form) to PFCP Session Context
	PFCPContext                         map[string]*PFCPSessionContext
	PDUSessionRelease_DUE_TO_DUP_PDU_ID bool

	// SM Policy related
	PCCRules           map[string]*PCCRule
	SessionRules       map[string]*SessionRule
	TrafficControlPool map[string]*TrafficControlData

	//PCO Related
	ProtocolConfigurationOptions *ProtocolConfigurationOptions
}

func canonicalName(identifier string, pduSessID int32) (canonical string) {
	return fmt.Sprintf("%s-%d", identifier, pduSessID)
}

func ResolveRef(identifier string, pduSessID int32) (ref string, err error) {
	ref, ok := canonicalRef[canonicalName(identifier, pduSessID)]
	if ok {
		err = nil
	} else {
		err = fmt.Errorf(
			"UE '%s' - PDUSessionID '%d' not found in SMContext", identifier, pduSessID)
	}
	return
}

func NewSMContext(identifier string, pduSessID int32) (smContext *SMContext) {
	smContext = new(SMContext)
	// Create Ref and identifier
	smContext.Ref = uuid.New().URN()
	smContextPool[smContext.Ref] = smContext
	canonicalRef[canonicalName(identifier, pduSessID)] = smContext.Ref

	smContext.Identifier = identifier
	smContext.PDUSessionID = pduSessID
	smContext.PFCPContext = make(map[string]*PFCPSessionContext)
	smContext.LocalSEID = GetSMContextCount()

	// initialize SM Policy Data
	smContext.PCCRules = make(map[string]*PCCRule)
	smContext.SessionRules = make(map[string]*SessionRule)
	smContext.TrafficControlPool = make(map[string]*TrafficControlData)
	smContext.ProtocolConfigurationOptions = &ProtocolConfigurationOptions{
		DNSIPv4Request: false,
		DNSIPv6Request: false,
	}
	return smContext
}

func GetSMContext(ref string) (smContext *SMContext) {
	smContext = smContextPool[ref]
	return smContext
}

func RemoveSMContext(ref string) {

	smContext := smContextPool[ref]

	for _, pfcpSessionContext := range smContext.PFCPContext {

		delete(seidSMContextMap, pfcpSessionContext.LocalSEID)
	}

	delete(smContextPool, ref)
}

func GetSMContextBySEID(SEID uint64) (smContext *SMContext) {
	smContext = seidSMContextMap[SEID]
	return smContext
}

func (smContext *SMContext) SetCreateData(createData *models.SmContextCreateData) {

	smContext.Gpsi = createData.Gpsi
	smContext.Supi = createData.Supi
	smContext.Dnn = createData.Dnn
	smContext.Snssai = createData.SNssai
	smContext.HplmnSnssai = createData.HplmnSnssai
	smContext.ServingNetwork = createData.ServingNetwork
	smContext.AnType = createData.AnType
	smContext.RatType = createData.RatType
	smContext.PresenceInLadn = createData.PresenceInLadn
	smContext.UeLocation = createData.UeLocation
	smContext.UeTimeZone = createData.UeTimeZone
	smContext.AddUeLocation = createData.AddUeLocation
	smContext.OldPduSessionId = createData.OldPduSessionId
	smContext.ServingNfId = createData.ServingNfId
}

func (smContext *SMContext) BuildCreatedData() (createdData *models.SmContextCreatedData) {
	createdData = new(models.SmContextCreatedData)
	createdData.SNssai = smContext.Snssai
	return
}

func (smContext *SMContext) PDUAddressToNAS() (addr [12]byte, addrLen uint8) {
	copy(addr[:], smContext.PDUAddress)
	switch smContext.SelectedPDUSessionType {
	case nasMessage.PDUSessionTypeIPv4:
		addrLen = 4 + 1
	case nasMessage.PDUSessionTypeIPv6:
	case nasMessage.PDUSessionTypeIPv4IPv6:
		addrLen = 12 + 1
	}
	return
}

// PCFSelection will select PCF for this SM Context
func (smContext *SMContext) PCFSelection() (err error) {

	// Send NFDiscovery for find PCF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	rep, res, err := SMF_Self().NFDiscoveryClient.NFInstancesStoreApi.SearchNFInstances(context.TODO(), models.NfType_PCF, models.NfType_SMF, &localVarOptionals)
	if err != nil {
		return
	}

	if res != nil {
		if status := res.StatusCode; status != http.StatusOK {
			apiError := err.(openapi.GenericOpenAPIError)
			problemDetails := apiError.Model().(models.ProblemDetails)

			logger.CtxLog.Warningf("NFDiscovery PCF return status: %d\n", status)
			logger.CtxLog.Warningf("Detail: %v\n", problemDetails.Title)
		}
	}

	// Select PCF from available PCF

	smContext.SelectedPCFProfile = rep.NfInstances[0]

	SelectedPCFProfileString, _ := json.MarshalIndent(smContext.SelectedPCFProfile, "", "  ")
	logger.CtxLog.Tracef("Select PCF Profile: %s\n", SelectedPCFProfileString)

	// Create SMPolicyControl Client for this SM Context
	for _, service := range *smContext.SelectedPCFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NPCF_SMPOLICYCONTROL {
			SmPolicyControlConf := Npcf_SMPolicyControl.NewConfiguration()
			SmPolicyControlConf.SetBasePath(service.ApiPrefix)
			smContext.SMPolicyClient = Npcf_SMPolicyControl.NewAPIClient(SmPolicyControlConf)
		}
	}

	return
}

func (smContext *SMContext) GetNodeIDByLocalSEID(seid uint64) (nodeID pfcpType.NodeID) {

	for _, pfcpCtx := range smContext.PFCPContext {
		if pfcpCtx.LocalSEID == seid {
			nodeID = pfcpCtx.NodeID
		}
	}

	return
}

func (smContext *SMContext) AllocateLocalSEIDForUPPath(path UPPath) {

	for _, upNode := range path {

		NodeIDtoIP := upNode.NodeID.ResolveNodeIdToIp().String()
		if _, exist := smContext.PFCPContext[NodeIDtoIP]; !exist {

			allocatedSEID := AllocateLocalSEID()
			smContext.PFCPContext[NodeIDtoIP] = &PFCPSessionContext{
				PDRs:      make(map[uint16]*PDR),
				NodeID:    upNode.NodeID,
				LocalSEID: allocatedSEID,
			}

			seidSMContextMap[allocatedSEID] = smContext
		}
	}
}

func (smContext *SMContext) AllocateLocalSEIDForDataPath(dataPath *DataPath) {
	logger.PduSessLog.Traceln("In AllocateLocalSEIDForDataPath")
	for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {

		NodeIDtoIP := curDataPathNode.UPF.NodeID.ResolveNodeIdToIp().String()
		logger.PduSessLog.Traceln("NodeIDtoIP: ", NodeIDtoIP)
		if _, exist := smContext.PFCPContext[NodeIDtoIP]; !exist {

			allocatedSEID := AllocateLocalSEID()
			smContext.PFCPContext[NodeIDtoIP] = &PFCPSessionContext{
				PDRs:      make(map[uint16]*PDR),
				NodeID:    curDataPathNode.UPF.NodeID,
				LocalSEID: allocatedSEID,
			}

			seidSMContextMap[allocatedSEID] = smContext
		}
	}
}

func (smContext *SMContext) PutPDRtoPFCPSession(nodeID pfcpType.NodeID, pdr *PDR) {

	NodeIDtoIP := nodeID.ResolveNodeIdToIp().String()
	pfcpSessionCtx := smContext.PFCPContext[NodeIDtoIP]
	pfcpSessionCtx.PDRs[pdr.PDRID] = pdr
}

func (smContext *SMContext) RemovePDRfromPFCPSession(nodeID pfcpType.NodeID, pdr *PDR) {

	NodeIDtoIP := nodeID.ResolveNodeIdToIp().String()
	pfcpSessionCtx := smContext.PFCPContext[NodeIDtoIP]
	delete(pfcpSessionCtx.PDRs, pdr.PDRID)
}

func (smContext *SMContext) isAllowedPDUSessionType(nasPDUSessionType uint8) bool {
	dnnPDUSessionType := smContext.DnnConfiguration.PduSessionTypes
	if dnnPDUSessionType == nil {
		logger.CtxLog.Errorf("this SMContext[%s] has no subscription pdu session type info\n", smContext.Ref)
		return false
	}

	allowIPv4 := false
	allowIPv6 := false
	allowEthernet := false

	for _, allowedPDUSessionType := range smContext.DnnConfiguration.PduSessionTypes.AllowedSessionTypes {
		switch allowedPDUSessionType {
		case models.PduSessionType_IPV4:
			allowIPv4 = true
		case models.PduSessionType_IPV6:
			allowIPv6 = true
		case models.PduSessionType_IPV4_V6:
			allowIPv4 = true
			allowIPv6 = true
		case models.PduSessionType_ETHERNET:
			allowEthernet = true
		}
	}

	switch nasConvert.PDUSessionTypeToModels(nasPDUSessionType) {
	case models.PduSessionType_IPV4:
		if allowIPv4 {
			return true
		} else {
			return false
		}

	case models.PduSessionType_IPV6:
		if allowIPv6 {
			return true
		} else {
			return false
		}
	case models.PduSessionType_IPV4_V6:
		if allowIPv4 || allowIPv6 {
			return true
		} else {
			return false
		}
	case models.PduSessionType_ETHERNET:
		if allowEthernet {
			return true
		} else {
			return false
		}
	default:
		return false
	}

}

// SM Policy related operation

// ApplySmPolicyFromDecision - update the SM Policy from the message
func (smContext *SMContext) ApplySmPolicyFromDecision(decision *models.SmPolicyDecision) error {
	for id, tcModel := range decision.TraffContDecs {
		tcData := NewTrafficControlDataFromModel(&tcModel)
		smContext.TrafficControlPool[id] = tcData
	}

	for id, sessRuleModel := range decision.SessRules {
		smContext.handleSessionRule(id, &sessRuleModel)
		// default activate first session rules
		smContext.SessionRules[id].isActivate = true
		break
	}

	for id, pccRuleModel := range decision.PccRules {
		smContext.handlePCCRule(id, &pccRuleModel)
	}

	return nil
}

func (smContext *SMContext) handlePCCRule(id string, pccRuleModel *models.PccRule) {
	if pccRuleModel == nil {
		smContext.removePCCRule(id)
	} else {
		// PCC rule installation
		if _, exist := smContext.PCCRules[id]; !exist {
			smContext.installPCCRule(id, pccRuleModel)
		} else { // PCC rule modification
			smContext.modifyPCCRule(id, pccRuleModel)
		}
	}
}

func (smContext *SMContext) installPCCRule(pccRuleID string, pccRuleModel *models.PccRule) error {
	logger.CtxLog.Debugf("Install PCCRule[%s]", pccRuleID)
	pccRule := NewPCCRuleFromModel(pccRuleModel)

	// set reference to traffic control data
	if len(pccRuleModel.RefTcData) != 0 && pccRuleModel.RefTcData[0] != "" {
		refTcID := pccRuleModel.RefTcData[0]
		tcData := smContext.TrafficControlPool[refTcID]
		if tcData != nil {
			tcData.RefedPCCRule[pccRuleID] = pccRule
		} else {
			return fmt.Errorf("pcc rule [%s] ref to traffic control data [%s] fail", pccRuleID, refTcID)
		}
	}

	smContext.PCCRules[pccRuleID] = pccRule
	return nil
}

func (smContext *SMContext) modifyPCCRule(pccRuleID string, pccRuleModel *models.PccRule) error {
	logger.CtxLog.Debugf("Modify PCCRule[%s]", pccRuleID)
	pccRule := NewPCCRuleFromModel(pccRuleModel)

	// set reference to traffic control data
	if len(pccRuleModel.RefTcData) != 0 && pccRuleModel.RefTcData[0] != "" {
		refTcID := pccRuleModel.RefTcData[0]
		tcData := smContext.TrafficControlPool[refTcID]
		if tcData != nil {
			tcData.RefedPCCRule[pccRuleID] = pccRule
		} else {
			return fmt.Errorf("pcc rule [%s] ref to traffic control data [%s] fail", pccRuleID, refTcID)
		}

		pccRule.RefTrafficControlData = tcData

	}

	smContext.PCCRules[pccRuleID] = pccRule
	return nil
}

func (smContext *SMContext) removePCCRule(id string) error {
	logger.CtxLog.Debugf("Remove PCCRule[%s]", id)
	pccRule, exist := smContext.PCCRules[id]
	if !exist {
		return fmt.Errorf("pcc rule [%s] not exist", id)
	}

	refTcData := pccRule.RefTrafficControlData
	delete(refTcData.RefedPCCRule, pccRule.PCCRuleID)

	delete(smContext.PCCRules, id)
	return nil
}

func (smContext *SMContext) handleSessionRule(id string, sessionRuleModel *models.SessionRule) {
	if sessionRuleModel == nil {
		logger.CtxLog.Debugf("Delete SessionRule[%s]", id)
		delete(smContext.SessionRules, id)
	} else {
		sessRule := NewSessionRuleFromModel(sessionRuleModel)
		// Session rule installation
		if oldSessRule, exist := smContext.SessionRules[id]; !exist {
			logger.CtxLog.Debugf("Install SessionRule[%s]", id)

			smContext.SessionRules[id] = sessRule
		} else { // Session rule modification
			logger.CtxLog.Debugf("Modify SessionRule[%s]", oldSessRule.SessionRuleID)
			smContext.SessionRules[id] = sessRule
		}
	}
}

// SelectedSessionRule - return the SMF selected session rule for this SM Context
func (smContext *SMContext) SelectedSessionRule() *SessionRule {
	for _, sessionRule := range smContext.SessionRules {
		if sessionRule.isActivate {
			return sessionRule
		}
	}

	return nil
}
