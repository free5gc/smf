package context

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antihax/optional"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/free5gc/nas/nasConvert"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/ngap/ngapType"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Namf_Communication"
	"github.com/free5gc/openapi/Nchf_ConvergedCharging"
	"github.com/free5gc/openapi/Nnrf_NFDiscovery"
	"github.com/free5gc/openapi/Npcf_SMPolicyControl"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

type DLForwardingType int

const (
	IndirectForwarding DLForwardingType = iota
	DirectForwarding
	NoForwarding
)

type UrrType int

// Reserved URR report for ID = 0 ~ 6
const (
	N3N6_MBQE_URR UrrType = iota
	N3N6_MAQE_URR
	N3N9_MBQE_URR
	N3N9_MAQE_URR
	N9N6_MBQE_URR
	N9N6_MAQE_URR
	NOT_FOUND_URR
)

func (t UrrType) String() string {
	urrTypeList := []string{"N3N6_MBQE", "N3N6_MAQE", "N3N9_MBQE", "N3N9_MAQE", "N9N6_MBQE", "N9N6_MAQE"}
	return urrTypeList[t]
}

func (t UrrType) IsBeforeQos() bool {
	urrTypeList := []bool{true, false, true, false, true, false}
	return urrTypeList[t]
}

func (t UrrType) Direct() string {
	urrTypeList := []string{"N3N6", "N3N6", "N3N9", "N3N9", "N9N6", "N9N6"}
	return urrTypeList[t]
}

var smContextCount uint64

type SMContextState uint32

const (
	InActive SMContextState = iota
	ActivePending
	Active
	InActivePending
	ModificationPending
	PFCPModification
)

const DefaultPrecedence uint32 = 255

func init() {
}

func GetSMContextCount() uint64 {
	return atomic.AddUint64(&smContextCount, 1)
}

type EventExposureNotification struct {
	*models.NsmfEventExposureNotification

	Uri string
}

type UsageReport struct {
	UrrId uint32
	UpfId uuid.UUID

	TotalVolume    uint64
	UplinkVolume   uint64
	DownlinkVolume uint64

	TotalPktNum    uint64
	UplinkPktNum   uint64
	DownlinkPktNum uint64

	ReportTpye models.TriggerType
}

var TeidGenerator *idgenerator.IDGenerator

type SMContext struct {
	*models.SmContextCreateData

	Ref string

	LocalSEID  uint64
	RemoteSEID uint64

	UnauthenticatedSupi bool

	Pei          string
	Identifier   string
	PDUSessionID int32

	LocalULTeid uint32
	LocalDLTeid uint32

	UpCnxState models.UpCnxState

	HoState models.HoState

	SelectionParam         *UPFSelectionParams
	PDUAddress             net.IP
	UseStaticIP            bool
	SelectedPDUSessionType uint8

	DnnConfiguration models.DnnConfiguration

	SMPolicyID string

	// Handover related
	DLForwardingType         DLForwardingType
	DLDirectForwardingTunnel *ngapType.UPTransportLayerInformation
	IndirectForwardingTunnel *DataPath

	// UP Security support TS 29.502 R16 6.1.6.2.39
	UpSecurity                                                     *models.UpSecurity
	MaximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink   models.MaxIntegrityProtectedDataRate
	MaximumDataRatePerUEForUserPlaneIntegrityProtectionForDownLink models.MaxIntegrityProtectedDataRate
	// SMF verified UP security result of Xn-handover TS 33.501 6.6.1
	UpSecurityFromPathSwitchRequestSameAsLocalStored bool

	// Client
	SMPolicyClient      *Npcf_SMPolicyControl.APIClient
	CommunicationClient *Namf_Communication.APIClient
	ChargingClient      *Nchf_ConvergedCharging.APIClient

	AMFProfile         models.NfProfile
	SelectedPCFProfile models.NfProfile
	SelectedCHFProfile models.NfProfile
	SmStatusNotifyUri  string

	Tunnel      *UPTunnel
	SelectedUPF *UPF
	BPManager   *BPManager

	// UPF UUID to PFCP Session Context
	PFCPSessionContexts map[uuid.UUID]*PFCPSessionContext

	PDUSessionRelease_DUE_TO_DUP_PDU_ID bool

	DNNInfo *SnssaiSmfDnnInfo

	// SM Policy related
	PCCRules            map[string]*PCCRule
	SessionRules        map[string]*SessionRule
	TrafficControlDatas map[string]*TrafficControlData
	ChargingData        map[string]*models.ChargingData
	QosDatas            map[string]*models.QosData

	UpPathChgEarlyNotification map[string]*EventExposureNotification // Key: Uri+NotifId
	UpPathChgLateNotification  map[string]*EventExposureNotification // Key: Uri+NotifId
	DataPathToBeRemoved        map[int64]*DataPath                   // Key: pathID

	SelectedSessionRuleID string

	// QoS
	QoSRuleIDGenerator      *idgenerator.IDGenerator
	PacketFilterIDGenerator *idgenerator.IDGenerator
	QFIGenerator            *idgenerator.IDGenerator
	PCCRuleIDToQoSRuleID    map[string]uint8
	qosDataToQFI            map[string]uint8
	PacketFilterIDToNASPFID map[string]uint8
	AMBRQerMap              map[uuid.UUID]uint32
	QerUpfMap               map[string]uint32
	AdditonalQosFlows       map[uint8]*QoSFlow // Key: qfi

	// URR
	UrrIDGenerator     *idgenerator.IDGenerator
	UrrIdMap           map[UrrType]uint32
	UrrUpfMap          map[string]*URR
	UrrReportTime      time.Duration
	UrrReportThreshold uint64
	// Cache the usage reports, sent from UPF
	// Those information will be included in CDR.
	UrrReports []UsageReport

	// Charging Related
	ChargingDataRef string
	// Each PDU session has a unique charging id
	ChargingID    int32
	RequestedUnit int32
	// key = urrid
	// All urr can map to a rating group
	// However, a rating group may map to more than one urr
	// e.g. In UL CL case, the rating group for recoreding PDU Session volume may map to two URR
	//		one is for PSA 1, the other is for PSA 2.
	ChargingInfo map[uint32]*ChargingInfo
	// NAS
	Pti                     uint8
	EstAcceptCause5gSMValue uint8

	// PCO Related
	ProtocolConfigurationOptions *ProtocolConfigurationOptions

	// State
	state SMContextState

	UeCmRegistered bool

	// Loggers
	Log *logrus.Entry

	// 5GSM Timers
	// T3591 is PDU SESSION MODIFICATION COMMAND timer
	T3591 *Timer
	// T3592 is PDU SESSION RELEASE COMMAND timer
	T3592 *Timer
}

func GenerateTEID() (uint32, error) {
	var id uint32
	if tmpID, err := TeidGenerator.Allocate(); err != nil {
		return 0, err
	} else {
		id = uint32(tmpID)
	}

	return id, nil
}

func ReleaseTEID(teid uint32) {
	TeidGenerator.FreeID(int64(teid))
}

func (smContext *SMContext) String() string {
	str := "SMContext {\n"
	prefix := "  "
	str += prefix + fmt.Sprintf("Ref: %s\n", smContext.Ref)
	str += prefix + fmt.Sprintf("State: %s\n", smContext.state)
	str += prefix + fmt.Sprintf("LocalSEID: %d\n", smContext.LocalSEID)
	str += prefix + fmt.Sprintf("RemoteSEID: %d\n", smContext.RemoteSEID)
	str += prefix + fmt.Sprintf("PDUSessionID: %d\n", smContext.PDUSessionID)
	str += prefix + fmt.Sprintf("PDUAddress %s\n", smContext.PDUAddress.String())

	str += prefix + fmt.Sprintf("%s\n", smContext.Tunnel)
	str += prefix + fmt.Sprintf("SelectedUPF %s\n", smContext.SelectedUPF.GetNodeIDString())
	for _, context := range smContext.PFCPSessionContexts {
		str += prefix + fmt.Sprintf("%s\n", context)
	}

	str += prefix + fmt.Sprintf("Pei: %s\n", smContext.Pei)
	str += prefix + fmt.Sprintf("Identifier (Supi): %s\n", smContext.Identifier)
	str += prefix + fmt.Sprintf("%s\n", smContext.SelectionParam)
	str += prefix + fmt.Sprintf("UseStaticIP %t\n", smContext.UseStaticIP)
	str += prefix + fmt.Sprintf("SelectedPDUSessionType %d\n", smContext.SelectedPDUSessionType)

	str += prefix + fmt.Sprintf("AMFProfile %s\n", smContext.AMFProfile.NfInstanceId)
	str += prefix + fmt.Sprintf("SelectedPCFProfile %s\n", smContext.SelectedPCFProfile.NfInstanceId)

	str += prefix + fmt.Sprintf("SMPolicyID: %s\n", smContext.SMPolicyID)
	str += prefix + fmt.Sprintf("DnnConfiguration: %+v\n", smContext.DnnConfiguration)
	str += prefix + fmt.Sprintf("UnauthenticatedSupi: %t\n", smContext.UnauthenticatedSupi)
	str += prefix + fmt.Sprintf("UpCnxState: %s\n", smContext.UpCnxState)
	str += prefix + fmt.Sprintf("HoState: %s\n", smContext.HoState)
	str += prefix + fmt.Sprintf("DLForwardingType: %d\n", smContext.DLForwardingType)
	str += prefix + fmt.Sprintf("DLDirectForwardingTunnel: %+v\n", smContext.DLDirectForwardingTunnel)
	str += prefix + fmt.Sprintf("IndirectForwardingTunnel: %s\n", smContext.IndirectForwardingTunnel)

	str += "}"
	return str
}

func NewSMContext(
	createData *models.SmContextCreateData,
	sessSubData []models.SessionManagementSubscriptionData,
) *SMContext {
	smContext := new(SMContext)

	smContext.SmContextCreateData = createData
	smContext.SmStatusNotifyUri = createData.SmContextStatusUri

	// Create Ref and identifier
	smContext.Ref = uuid.New().URN()
	smfContext.SmContextPool.Store(smContext.Ref, smContext)
	smfContext.CanonicalRef.Store(canonicalName(createData.Supi, createData.PduSessionId), smContext.Ref)

	smContext.Log = logger.PduSessLog.WithFields(logrus.Fields{
		logger.FieldSupi:         createData.Supi,
		logger.FieldPDUSessionID: fmt.Sprintf("%d", createData.PduSessionId),
	})

	smContext.SetState(InActive)
	smContext.Identifier = createData.Supi
	smContext.PDUSessionID = createData.PduSessionId
	smContext.PFCPSessionContexts = make(map[uuid.UUID]*PFCPSessionContext)

	smContext.LocalSEID = GetSMContextCount()

	// initialize SM Policy Data
	smContext.PCCRules = make(map[string]*PCCRule)
	smContext.SessionRules = make(map[string]*SessionRule)
	smContext.TrafficControlDatas = make(map[string]*TrafficControlData)
	smContext.QosDatas = make(map[string]*models.QosData)
	smContext.UpPathChgEarlyNotification = make(map[string]*EventExposureNotification)
	smContext.UpPathChgLateNotification = make(map[string]*EventExposureNotification)
	smContext.DataPathToBeRemoved = make(map[int64]*DataPath)

	smContext.ProtocolConfigurationOptions = &ProtocolConfigurationOptions{}

	smContext.BPManager = NewBPManager(createData.Supi)
	smContext.Tunnel = NewUPTunnel()

	smContext.QoSRuleIDGenerator = idgenerator.NewGenerator(1, 255)
	smContext.PacketFilterIDGenerator = idgenerator.NewGenerator(1, 255)
	smContext.QFIGenerator = idgenerator.NewGenerator(2, 63) // 1 always reserve for default Qos
	smContext.PCCRuleIDToQoSRuleID = make(map[string]uint8)
	smContext.PacketFilterIDToNASPFID = make(map[string]uint8)
	smContext.qosDataToQFI = make(map[string]uint8)
	smContext.AMBRQerMap = make(map[uuid.UUID]uint32)
	smContext.QerUpfMap = make(map[string]uint32)
	smContext.AdditonalQosFlows = make(map[uint8]*QoSFlow)
	smContext.UrrIDGenerator = idgenerator.NewGenerator(1, math.MaxUint32)
	smContext.UrrIdMap = make(map[UrrType]uint32)
	smContext.GenerateUrrId()
	smContext.UrrUpfMap = make(map[string]*URR)

	smContext.ChargingInfo = make(map[uint32]*ChargingInfo)
	smContext.ChargingID = GenerateChargingID()

	if factory.SmfConfig != nil &&
		factory.SmfConfig.Configuration != nil {
		smContext.UrrReportTime = time.Duration(factory.SmfConfig.Configuration.UrrPeriod) * time.Second
		smContext.UrrReportThreshold = factory.SmfConfig.Configuration.UrrThreshold
		logger.CtxLog.Debugf("UrrPeriod: %v", smContext.UrrReportTime)
		logger.CtxLog.Debugf("UrrThreshold: %d", smContext.UrrReportThreshold)
		if factory.SmfConfig.Configuration.RequestedUnit != 0 {
			smContext.RequestedUnit = factory.SmfConfig.Configuration.RequestedUnit
		} else {
			smContext.RequestedUnit = 1000
		}
	}

	// DNN Information from config
	smContext.DNNInfo = RetrieveDnnInformation(createData.SNssai, createData.Dnn)
	if smContext.DNNInfo == nil {
		logger.PduSessLog.Errorf("S-NSSAI[sst: %d, sd: %s] DNN[%s] not matched DNN Config",
			smContext.SNssai.Sst, smContext.SNssai.Sd, smContext.Dnn)
	}
	logger.PduSessLog.Debugf("S-NSSAI[sst: %d, sd: %s] DNN[%s]",
		smContext.SNssai.Sst, smContext.SNssai.Sd, smContext.Dnn)

	smContext.DnnConfiguration = sessSubData[0].DnnConfigurations[smContext.Dnn]
	// UP Security info present in session management subscription data
	if smContext.DnnConfiguration.UpSecurity != nil {
		smContext.UpSecurity = smContext.DnnConfiguration.UpSecurity
	}

	var err error
	smContext.LocalDLTeid, err = GenerateTEID()
	if err != nil {
		return nil
	}

	smContext.LocalULTeid, err = GenerateTEID()
	if err != nil {
		return nil
	}

	return smContext
}

func (smContext *SMContext) GenerateUrrId() {
	if id, err := smContext.UrrIDGenerator.Allocate(); err == nil {
		smContext.UrrIdMap[N3N6_MBQE_URR] = uint32(id)
	}
	if id, err := smContext.UrrIDGenerator.Allocate(); err == nil {
		smContext.UrrIdMap[N3N6_MAQE_URR] = uint32(id)
	}
	if id, err := smContext.UrrIDGenerator.Allocate(); err == nil {
		smContext.UrrIdMap[N9N6_MBQE_URR] = uint32(id)
	}
	if id, err := smContext.UrrIDGenerator.Allocate(); err == nil {
		smContext.UrrIdMap[N9N6_MAQE_URR] = uint32(id)
	}
	if id, err := smContext.UrrIDGenerator.Allocate(); err == nil {
		smContext.UrrIdMap[N3N9_MBQE_URR] = uint32(id)
	}
	if id, err := smContext.UrrIDGenerator.Allocate(); err == nil {
		smContext.UrrIdMap[N3N9_MAQE_URR] = uint32(id)
	}
}

func (smContext *SMContext) BuildCreatedData() *models.SmContextCreatedData {
	return &models.SmContextCreatedData{
		SNssai: smContext.SNssai,
	}
}

func (smContext *SMContext) SetState(state SMContextState) {
	oldState := SMContextState(atomic.LoadUint32((*uint32)(&smContext.state)))

	atomic.StoreUint32((*uint32)(&smContext.state), uint32(state))
	smContext.Log.Tracef("State[%s] -> State[%s]", oldState, state)
}

func (smContext *SMContext) CheckState(state SMContextState) bool {
	curState := SMContextState(atomic.LoadUint32((*uint32)(&smContext.state)))
	check := curState == state
	if !check {
		smContext.Log.Warnf("Unexpected state, expect: [%s], actual:[%s]", state, curState)
	}
	return check
}

func (smContext *SMContext) State() SMContextState {
	return SMContextState(atomic.LoadUint32((*uint32)(&smContext.state)))
}

func (smContext *SMContext) PDUAddressToNAS() ([12]byte, uint8) {
	var addr [12]byte
	var addrLen uint8
	copy(addr[:], smContext.PDUAddress)
	switch smContext.SelectedPDUSessionType {
	case nasMessage.PDUSessionTypeIPv4:
		addrLen = 4 + 1
	case nasMessage.PDUSessionTypeIPv6:
	case nasMessage.PDUSessionTypeIPv4IPv6:
		addrLen = 12 + 1
	}
	return addr, addrLen
}

func (smContext *SMContext) SetServingAMF(amf *models.NfProfile) error {
	if amf.NfType != models.NfType_AMF {
		return fmt.Errorf("cannot set AMF to NF of type %s", amf.NfType)
	}
	smContext.AMFProfile = *amf
	// Create AMF Comm Client for this SM Context
	for _, service := range *smContext.AMFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NAMF_COMM {
			communicationConf := Namf_Communication.NewConfiguration()
			communicationConf.SetBasePath(service.ApiPrefix)
			smContext.CommunicationClient = Namf_Communication.NewAPIClient(communicationConf)
		}
	}
	return nil
}

func (smContext *SMContext) SetServingPCF(pcf *models.NfProfile) error {
	if pcf.NfType != models.NfType_PCF {
		return fmt.Errorf("cannot set PCF to NF of type %s", pcf.NfType)
	}
	smContext.SelectedPCFProfile = *pcf

	// Create SMPolicyControl Client for this SM Context
	for _, service := range *smContext.SelectedPCFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NPCF_SMPOLICYCONTROL {
			SmPolicyControlConf := Npcf_SMPolicyControl.NewConfiguration()
			SmPolicyControlConf.SetBasePath(service.ApiPrefix)
			smContext.SMPolicyClient = Npcf_SMPolicyControl.NewAPIClient(SmPolicyControlConf)
		}
	}

	return nil
}

// CHFSelection will select CHF for this SM Context
func (smContext *SMContext) CHFSelection() error {
	// Send NFDiscovery for find CHF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{
		// Supi: optional.NewString(smContext.Supi),
	}

	ctx, _, err := smfContext.GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return err
	}

	rsp, res, err := GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_CHF, models.NfType_SMF, &localVarOptionals)
	if err != nil {
		return err
	}
	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.PduSessLog.Errorf("SmfEventExposureNotification response body cannot close: %+v", rspCloseErr)
		}
	}()

	if res != nil {
		if status := res.StatusCode; status != http.StatusOK {
			apiError := err.(openapi.GenericOpenAPIError)
			problemDetails := apiError.Model().(models.ProblemDetails)

			logger.CtxLog.Warningf("NFDiscovery SMF return status: %d\n", status)
			logger.CtxLog.Warningf("Detail: %v\n", problemDetails.Title)
		}
	}

	// Select CHF from available CHF

	smContext.SelectedCHFProfile = rsp.NfInstances[0]

	// Create Converged Charging Client for this SM Context
	for _, service := range *smContext.SelectedCHFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NCHF_CONVERGEDCHARGING {
			ConvergedChargingConf := Nchf_ConvergedCharging.NewConfiguration()
			ConvergedChargingConf.SetBasePath(service.ApiPrefix)
			smContext.ChargingClient = Nchf_ConvergedCharging.NewAPIClient(ConvergedChargingConf)
		}
	}

	return nil
}

// PCFSelection will select PCF for this SM Context
func (smContext *SMContext) PCFSelection() error {
	ctx, _, err := GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, "NRF")
	if err != nil {
		return err
	}
	// Send NFDiscovery for find PCF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	if GetSelf().Locality != "" {
		localVarOptionals.PreferredLocality = optional.NewString(GetSelf().Locality)
	}

	rsp, res, err := GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_PCF, models.NfType_SMF, &localVarOptionals)
	if err != nil {
		return err
	}
	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.PduSessLog.Errorf("SmfEventExposureNotification response body cannot close: %+v", rspCloseErr)
		}
	}()

	if res != nil {
		if status := res.StatusCode; status != http.StatusOK {
			apiError := err.(openapi.GenericOpenAPIError)
			problemDetails := apiError.Model().(models.ProblemDetails)

			logger.CtxLog.Warningf("NFDiscovery PCF return status: %d\n", status)
			logger.CtxLog.Warningf("Detail: %v\n", problemDetails.Title)
		}
	}

	// Select PCF from available PCF

	smContext.SelectedPCFProfile = rsp.NfInstances[0]

	// Create SMPolicyControl Client for this SM Context
	for _, service := range *smContext.SelectedPCFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NPCF_SMPOLICYCONTROL {
			SmPolicyControlConf := Npcf_SMPolicyControl.NewConfiguration()
			SmPolicyControlConf.SetBasePath(service.ApiPrefix)
			smContext.SMPolicyClient = Npcf_SMPolicyControl.NewAPIClient(SmPolicyControlConf)
		}
	}

	return nil
}

func (smContext *SMContext) GetNodeIDByLocalSEID(seid uint64) pfcpType.NodeID {
	for _, pfcpCtx := range smContext.PFCPSessionContexts {
		if pfcpCtx.LocalSEID == seid {
			return pfcpCtx.UPF.NodeID
		}
	}

	return pfcpType.NodeID{}
}

func (smContext *SMContext) AllocateLocalSEIDForDataPath(dataPath *DataPath) {
	for node := dataPath.FirstDPNode; node != nil; node = node.Next() {
		uuid := node.UPF.GetID()
		if _, exist := smContext.PFCPSessionContexts[uuid]; !exist {
			allocatedSEID := AllocateLocalSEID()

			logger.PduSessLog.Debugf("Allocated local SEID %d for UPF[%s]", allocatedSEID, node.UPF.GetNodeIDString())

			newPFCPSessionContext := &PFCPSessionContext{
				PDRs:         make(map[uint16]*PDR),
				UPF:          node.UPF,
				PDUSessionID: smContext.PDUSessionID,
				UeIP:         smContext.PDUAddress,
				LocalSEID:    allocatedSEID,
			}

			smContext.PFCPSessionContexts[uuid] = newPFCPSessionContext
			node.UPF.PFCPSessionContexts[newPFCPSessionContext.LocalSEID] = newPFCPSessionContext
			smfContext.SeidSMContextMap.Store(allocatedSEID, smContext)
		}
	}
}

type RecoverPDR struct {
	PDR                *PDR
	State              RuleState
	PFCPSessionContext *PFCPSessionContext
}

func (smContext *SMContext) AddPDRtoPFCPSession(upf *UPF, pdr *PDR) error {
	if pfcpSessionContext, exist := smContext.PFCPSessionContexts[upf.GetID()]; !exist {
		return fmt.Errorf("cannot find PFCPContext for UPF[%s] to put PDR %d", upf.GetNodeIDString(), pdr.PDRID)
	} else {
		pfcpSessionContext.PDRs[pdr.PDRID] = pdr // store the same PDR reference for primary and secondary UPF
		return nil
	}
}

func (c *SMContext) findPSAandAllocUeIP(param *UPFSelectionParams) error {
	c.Log.Traceln("findPSAandAllocUeIP")
	if param == nil {
		return fmt.Errorf("UPFSelectionParams is nil")
	}

	upi := GetUserPlaneInformation()
	if GetSelf().ULCLSupport && CheckUEHasPreConfig(c.Supi) {
		groupName := GetULCLGroupNameFromSUPI(c.Supi)
		preConfigPathPool := GetUEDefaultPathPool(groupName)
		if preConfigPathPool != nil {
			selectedUPFName := ""
			selectedUPFName, c.PDUAddress, c.UseStaticIP = preConfigPathPool.SelectUPFAndAllocUEIPForULCL(
				upi, param)
			c.SelectedUPF = upi.NameToUPNode[selectedUPFName].(*UPF)
		}
	} else {
		// single UPF case
		var err error
		c.SelectedUPF, c.PDUAddress, c.UseStaticIP, err = upi.SelectUPFAndAllocUEIP(param)
		if err != nil {
			return fmt.Errorf("failed to find PSA and allocate PDU address: %v", err)
		}
	}
	if c.SelectedUPF == nil {
		return fmt.Errorf("failed to select PSA UPF, selection parameters: %s", param.String())
	}
	if c.PDUAddress == nil {
		return fmt.Errorf("failed to allocate PDU address, selection parameters: %s", param.String())
	}

	return nil
}

func (c *SMContext) AllocUeIP() error {
	c.SelectionParam = &UPFSelectionParams{
		Dnn: c.Dnn,
		SNssai: &SNssai{
			Sst: c.SNssai.Sst,
			Sd:  c.SNssai.Sd,
		},
	}

	if len(c.DnnConfiguration.StaticIpAddress) > 0 {
		staticIPConfig := c.DnnConfiguration.StaticIpAddress[0]
		if staticIPConfig.Ipv4Addr != "" {
			c.SelectionParam.PDUAddress = net.ParseIP(staticIPConfig.Ipv4Addr).To4()
		}
	}

	if err := c.findPSAandAllocUeIP(c.SelectionParam); err != nil {
		return err
	}
	return nil
}

// This function create a data path to be default data path.
func (c *SMContext) SelectDefaultDataPath() error {
	if c.SelectionParam == nil || c.SelectedUPF == nil {
		return fmt.Errorf("SelectDefaultDataPath err: SelectionParam or SelectedUPF is nil")
	}

	var defaultPath *DataPath
	if GetSelf().ULCLSupport && CheckUEHasPreConfig(c.Supi) {
		c.Log.Infof("Has pre-config route")
		uePreConfigPaths := GetUEPreConfigPaths(c.Supi, c.SelectedUPF.Name)
		c.Tunnel.DataPathPool = uePreConfigPaths.DataPathPool
		c.Tunnel.PathIDGenerator = uePreConfigPaths.PathIDGenerator
		defaultPath = c.Tunnel.DataPathPool.GetDefaultPath()
	} else if c.Tunnel.DataPathPool.GetDefaultPath() == nil {
		// UE has no pre-config path and default path
		// Use default route
		c.Log.Infof("Has no pre-config route. Has no default path")
		defaultUPPath := GetUserPlaneInformation().GetDefaultUserPlanePathByDNNAndUPF(c.SelectionParam, c.SelectedUPF)
		c.Log.Traceln("[GenerateDataPath] Generated defaultUPPath ", defaultUPPath)
		defaultPath = GenerateDataPath(defaultUPPath)
		c.Log.Traceln("[GenerateDataPath] Successfully generated data path ", defaultPath)
		if defaultPath != nil {
			defaultPath.IsDefaultPath = true
			c.Log.Traceln("[GenerateDataPath] Adding data path to tunnel")
			c.Tunnel.AddDataPath(defaultPath)
			c.Log.Traceln("[GenerateDataPath] Successfully added data path to tunnel")
		}
	} else {
		c.Log.Infof("Has no pre-config route. Has default path")
		defaultPath = c.Tunnel.DataPathPool.GetDefaultPath()
	}

	if defaultPath == nil {
		return fmt.Errorf("data path not found, Selection Parameter: %s",
			c.SelectionParam.String())
	}

	if !defaultPath.Activated {
		c.Log.Traceln("[GenerateDataPath] Activating tunnel and PDR for generated data path")
		defaultPath.ActivateTunnelAndPDR(c, DefaultPrecedence)
		c.Log.Traceln("[GenerateDataPath] Successfully activated tunnel and PDR for generated data path")
	}

	return nil
}

func (c *SMContext) CreatePccRuleDataPath(pccRule *PCCRule,
	tcData *TrafficControlData, qosData *models.QosData,
	chgData *models.ChargingData,
) error {
	var targetRoute models.RouteToLocation
	if tcData != nil && len(tcData.RouteToLocs) > 0 {
		targetRoute = tcData.RouteToLocs[0]
	}
	param := &UPFSelectionParams{
		Dnn: c.Dnn,
		SNssai: &SNssai{
			Sst: c.SNssai.Sst,
			Sd:  c.SNssai.Sd,
		},
		Dnai: targetRoute.Dnai,
	}
	createdUpPath := GetUserPlaneInformation().GetDefaultUserPlanePathByDNN(param)
	createdDataPath := GenerateDataPath(createdUpPath)
	if createdDataPath == nil {
		return fmt.Errorf("fail to create data path for pcc rule[%s]", pccRule.PccRuleId)
	}

	// Try to use a default pcc rule as default data path
	if c.Tunnel.DataPathPool.GetDefaultPath() == nil &&
		pccRule.Precedence == 255 {
		createdDataPath.IsDefaultPath = true
	}

	createdDataPath.GBRFlow = isGBRFlow(qosData)
	createdDataPath.ActivateTunnelAndPDR(c, uint32(pccRule.Precedence))
	c.Tunnel.AddDataPath(createdDataPath)
	pccRule.Datapath = createdDataPath
	pccRule.AddDataPathForwardingParameters(c, &targetRoute)

	if chgLevel, err := pccRule.IdentifyChargingLevel(); err != nil {
		c.Log.Warnf("fail to identify charging level[%+v] for pcc rule[%s]", err, pccRule.PccRuleId)
	} else {
		pccRule.Datapath.AddChargingRules(c, chgLevel, chgData)
	}

	pccRule.Datapath.AddQoS(c, pccRule.QFI, qosData)
	c.AddQosFlow(pccRule.QFI, qosData)
	return nil
}

func (c *SMContext) BuildUpPathChgEventExposureNotification(
	chgEvent *models.UpPathChgEvent,
	srcRoute, tgtRoute *models.RouteToLocation,
) {
	if chgEvent == nil {
		return
	}
	if chgEvent.NotificationUri == "" {
		c.Log.Warnf("No NotificationUri [%s]", chgEvent.NotificationUri)
		return
	}

	en := models.EventNotification{
		Event:            models.SmfEvent_UP_PATH_CH,
		SourceTraRouting: srcRoute,
		TargetTraRouting: tgtRoute,
	}
	if srcRoute.Dnai != tgtRoute.Dnai {
		en.SourceDnai = srcRoute.Dnai
		en.TargetDnai = tgtRoute.Dnai
	}
	// TODO: sourceUeIpv4Addr, sourceUeIpv6Prefix, targetUeIpv4Addr, targetUeIpv6Prefix

	k := chgEvent.NotificationUri + chgEvent.NotifCorreId
	if strings.Contains(string(chgEvent.DnaiChgType), "EARLY") {
		en.DnaiChgType = models.DnaiChangeType("EARLY")
		v, ok := c.UpPathChgEarlyNotification[k]
		if ok {
			v.EventNotifs = append(v.EventNotifs, en)
		} else {
			c.UpPathChgEarlyNotification[k] = newEventExposureNotification(
				chgEvent.NotificationUri, chgEvent.NotifCorreId, en)
		}
	}
	if strings.Contains(string(chgEvent.DnaiChgType), "LATE") {
		en.DnaiChgType = models.DnaiChangeType("LATE")
		v, ok := c.UpPathChgLateNotification[k]
		if ok {
			v.EventNotifs = append(v.EventNotifs, en)
		} else {
			c.UpPathChgLateNotification[k] = newEventExposureNotification(
				chgEvent.NotificationUri, chgEvent.NotifCorreId, en)
		}
	}
}

func newEventExposureNotification(
	uri, id string,
	en models.EventNotification,
) *EventExposureNotification {
	return &EventExposureNotification{
		NsmfEventExposureNotification: &models.NsmfEventExposureNotification{
			NotifId:     id,
			EventNotifs: []models.EventNotification{en},
		},
		Uri: uri,
	}
}

type NotifCallback func(uri string,
	notification *models.NsmfEventExposureNotification)

func (c *SMContext) SendUpPathChgNotification(chgType string, notifCb NotifCallback) {
	var notifications map[string]*EventExposureNotification
	if chgType == "EARLY" {
		notifications = c.UpPathChgEarlyNotification
	} else if chgType == "LATE" {
		notifications = c.UpPathChgLateNotification
	} else {
		return
	}
	for k, n := range notifications {
		c.Log.Infof("Send UpPathChg Event Exposure Notification [%s][%s] to NEF/AF", chgType, n.NotifId)
		go notifCb(n.Uri, n.NsmfEventExposureNotification)
		delete(notifications, k)
	}
}

func (smContext *SMContext) UpdateANInformation(ip net.IP, teid uint32) {
	tunnel := smContext.Tunnel
	tunnel.ANInformation = &ANInformation{
		IPAddress: ip,
		TEID:      teid,
	}

	for _, dataPath := range tunnel.DataPathPool {
		if dataPath.Activated {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR

			if DLPDR.FAR.ForwardingParameters.OuterHeaderCreation != nil {
				// Old AN tunnel exists
				DLPDR.FAR.ForwardingParameters.SendEndMarker = true
			}

			DLPDR.FAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
			dlOuterHeaderCreation := DLPDR.FAR.ForwardingParameters.OuterHeaderCreation
			dlOuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
			dlOuterHeaderCreation.Teid = tunnel.ANInformation.TEID
			dlOuterHeaderCreation.Ipv4Address = tunnel.ANInformation.IPAddress.To4()
			DLPDR.FAR.SetState(RULE_UPDATE)
		}
	}
}

func (smContext *SMContext) MarkPDRsAsRemove(dataPath *DataPath) {
	for curDPNode := dataPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if curDPNode.DownLinkTunnel != nil && curDPNode.DownLinkTunnel.PDR != nil {
			curDPNode.DownLinkTunnel.PDR.SetState(RULE_REMOVE)
			curDPNode.DownLinkTunnel.PDR.FAR.SetState(RULE_REMOVE)
		}
		if curDPNode.UpLinkTunnel != nil && curDPNode.UpLinkTunnel.PDR != nil {
			curDPNode.UpLinkTunnel.PDR.SetState(RULE_REMOVE)
			curDPNode.UpLinkTunnel.PDR.FAR.SetState(RULE_REMOVE)
		}
	}
}

func (smContext *SMContext) RemovePDRfromPFCPSession(uuid uuid.UUID, pdr *PDR) {
	pfcpSessCtx := smContext.PFCPSessionContexts[uuid]
	delete(pfcpSessCtx.PDRs, pdr.PDRID)
}

func (smContext *SMContext) IsAllowedPDUSessionType(requestedPDUSessionType uint8) error {
	dnnPDUSessionType := smContext.DnnConfiguration.PduSessionTypes
	if dnnPDUSessionType == nil {
		return fmt.Errorf("this SMContext[%s] has no subscription pdu session type info", smContext.Ref)
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

	supportedPDUSessionType := GetSelf().SupportedPDUSessionType
	switch supportedPDUSessionType {
	case "IPv4":
		if !allowIPv4 {
			return fmt.Errorf(
				"No SupportedPDUSessionType[%q] in DNN[%s] configuration",
				supportedPDUSessionType,
				smContext.Dnn,
			)
		}
	case "IPv6":
		if !allowIPv6 {
			return fmt.Errorf(
				"No SupportedPDUSessionType[%q] in DNN[%s] configuration",
				supportedPDUSessionType,
				smContext.Dnn,
			)
		}
	case "IPv4v6":
		if !allowIPv4 && !allowIPv6 {
			return fmt.Errorf(
				"No SupportedPDUSessionType[%q] in DNN[%s] configuration",
				supportedPDUSessionType,
				smContext.Dnn,
			)
		}
	case "Ethernet":
		if !allowEthernet {
			return fmt.Errorf(
				"No SupportedPDUSessionType[%q] in DNN[%s] configuration",
				supportedPDUSessionType,
				smContext.Dnn,
			)
		}
	}

	smContext.EstAcceptCause5gSMValue = 0
	switch nasConvert.PDUSessionTypeToModels(requestedPDUSessionType) {
	case models.PduSessionType_IPV4:
		if allowIPv4 {
			smContext.SelectedPDUSessionType = nasConvert.ModelsToPDUSessionType(models.PduSessionType_IPV4)
		} else {
			return fmt.Errorf("PduSessionType_IPV4 is not allowed in DNN[%s] configuration", smContext.Dnn)
		}
	case models.PduSessionType_IPV6:
		if allowIPv6 {
			smContext.SelectedPDUSessionType = nasConvert.ModelsToPDUSessionType(models.PduSessionType_IPV6)
		} else {
			return fmt.Errorf("PduSessionType_IPV6 is not allowed in DNN[%s] configuration", smContext.Dnn)
		}
	case models.PduSessionType_IPV4_V6:
		if allowIPv4 && allowIPv6 {
			smContext.SelectedPDUSessionType = nasConvert.ModelsToPDUSessionType(models.PduSessionType_IPV4_V6)
		} else if allowIPv4 {
			smContext.SelectedPDUSessionType = nasConvert.ModelsToPDUSessionType(models.PduSessionType_IPV4)
			smContext.EstAcceptCause5gSMValue = nasMessage.Cause5GSMPDUSessionTypeIPv4OnlyAllowed
		} else if allowIPv6 {
			smContext.SelectedPDUSessionType = nasConvert.ModelsToPDUSessionType(models.PduSessionType_IPV6)
			smContext.EstAcceptCause5gSMValue = nasMessage.Cause5GSMPDUSessionTypeIPv6OnlyAllowed
		} else {
			return fmt.Errorf("PduSessionType_IPV4_V6 is not allowed in DNN[%s] configuration", smContext.Dnn)
		}
	case models.PduSessionType_ETHERNET:
		if allowEthernet {
			smContext.SelectedPDUSessionType = nasConvert.ModelsToPDUSessionType(models.PduSessionType_ETHERNET)
		} else {
			return fmt.Errorf("PduSessionType_ETHERNET is not allowed in DNN[%s] configuration", smContext.Dnn)
		}
	default:
		return fmt.Errorf("Requested PDU Sesstion type[%d] is not supported", requestedPDUSessionType)
	}
	return nil
}

func (smContext *SMContext) StopT3591() {
	if smContext.T3591 != nil {
		smContext.T3591.Stop()
		smContext.T3591 = nil
	}
}

func (smContext *SMContext) StopT3592() {
	if smContext.T3592 != nil {
		smContext.T3592.Stop()
		smContext.T3592 = nil
	}
}

func (smContextState SMContextState) String() string {
	switch smContextState {
	case InActive:
		return "InActive"
	case ActivePending:
		return "ActivePending"
	case Active:
		return "Active"
	case InActivePending:
		return "InActivePending"
	case ModificationPending:
		return "ModificationPending"
	case PFCPModification:
		return "PFCPModification"
	default:
		return "Unknown State"
	}
}

func (smContext *SMContext) AssignQFI(qosId string) uint8 {
	qfi, ok := smContext.qosDataToQFI[qosId]
	if !ok {
		newId, err := smContext.QFIGenerator.Allocate()
		if err != nil {
			return 0
		}
		smContext.qosDataToQFI[qosId] = uint8(newId)
		return uint8(newId)
	}
	return qfi
}

func (smContext *SMContext) RemoveQFI(qosId string) {
	qfi, ok := smContext.qosDataToQFI[qosId]
	if ok {
		smContext.QFIGenerator.FreeID(int64(qfi))
		delete(smContext.qosDataToQFI, qosId)
		smContext.RemoveQosFlow(qfi)
	}
}
