package consumer

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/pcf/SMPolCtrl"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/util/flowdesc"
	sbi_metrics "github.com/free5gc/util/metrics/sbi"
)

type npcfService struct {
	consumer *Consumer

	SMPolicyControlMu sync.RWMutex

	SMPolicyControlClients map[string]*SMPolCtrl.APIClient
}

func (s *npcfService) getSMPolicyControlClient(uri string) *SMPolCtrl.APIClient {
	if uri == "" {
		return nil
	}
	s.SMPolicyControlMu.RLock()
	client, ok := s.SMPolicyControlClients[uri]
	if ok {
		s.SMPolicyControlMu.RUnlock()
		return client
	}

	configuration := SMPolCtrl.NewConfiguration()
	configuration.SetBasePath(uri)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	client = SMPolCtrl.NewAPIClient(configuration)

	s.SMPolicyControlMu.RUnlock()
	s.SMPolicyControlMu.Lock()
	defer s.SMPolicyControlMu.Unlock()
	s.SMPolicyControlClients[uri] = client
	return client
}

// SendSMPolicyAssociationCreate create the session management association to the PCF
func (s *npcfService) SendSMPolicyAssociationCreate(smContext *smf_context.SMContext) (
	string, *models.Pcf_SMPolCtrl_SmPolicyDecision, error,
) {
	var client *SMPolCtrl.APIClient

	// Create SMPolicyControl Client for this SM Context
	for _, service := range smContext.SelectedPCFProfile.NfServices {
		if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL {
			client = s.getSMPolicyControlClient(service.ApiPrefix)
		}
	}

	if client == nil {
		return "", nil, errors.Errorf("smContext not selected PCF")
	}

	smPolicyData := models.Pcf_SMPolCtrl_SmPolicyContextData{}

	smPolicyData.Supi = smContext.Supi
	smPolicyData.PduSessionId = smContext.PDUSessionID
	smPolicyData.NotificationUri = fmt.Sprintf("%s://%s:%d/nsmf-callback/v1/sm-policies/%s",
		smf_context.GetSelf().URIScheme,
		smf_context.GetSelf().RegisterIPv4,
		smf_context.GetSelf().SBIPort,
		smContext.Ref,
	)
	smPolicyData.Dnn = smContext.Dnn
	smPolicyData.PduSessionType = smf_context.PDUSessionTypeToModels(smContext.SelectedPDUSessionType)
	smPolicyData.AccessType = smContext.AnType
	smPolicyData.RatType = smContext.RatType
	smPolicyData.Ipv4Address = smContext.PDUAddress.To4().String()
	smPolicyData.SubsSessAmbr = smContext.DnnConfiguration.SessionAmbr
	smPolicyData.SubsDefQos = smContext.DnnConfiguration.Var5gQosProfile
	smPolicyData.SliceInfo = smContext.SNssai
	smPolicyData.ServingNetwork = &models.PlmnIdNid{
		Mcc: smContext.ServingNetwork.Mcc,
		Mnc: smContext.ServingNetwork.Mnc,
	}
	smPolicyData.SuppFeat = "F"
	if smContext.UeLocation != nil {
		ueLocation := *smContext.UeLocation
		if ueLocation.NrLocation != nil && ueLocation.NrLocation.AgeOfLocationInformation < 0 {
			nrLoc := *ueLocation.NrLocation
			nrLoc.AgeOfLocationInformation = 0
			ueLocation.NrLocation = &nrLoc
		}
		smPolicyData.UserLocationInfo = &ueLocation
	}

	ctx, _, err := smf_context.GetSelf().
		GetTokenCtxForNFInstance(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
			models.Nrf_NFMgmt_NFType_PCF, smContext.SelectedPCFProfile.NfInstanceId)
	if err != nil {
		return "", nil, err
	}

	var smPolicyID string
	var smPolicyDecision *models.Pcf_SMPolCtrl_SmPolicyDecision
	request := &SMPolCtrl.CreateSMPolicyRequest{
		RequestBody: &smPolicyData,
	}

	smPolicyDecisionFromPCF, err := client.SMPoliciesCollectionApi.CreateSMPolicy(ctx, request)
	if err != nil || smPolicyDecisionFromPCF == nil {
		return "", nil, err
	}

	smPolicyDecision = smPolicyDecisionFromPCF.Pcf_SMPolCtrl_SmPolicyDecision
	loc := smPolicyDecisionFromPCF.Location
	if smPolicyID = s.extractSMPolicyIDFromLocation(loc); len(smPolicyID) == 0 {
		return "", nil, fmt.Errorf("SMPolicy ID parse failed")
	}
	return smPolicyID, smPolicyDecision, nil
}

var smPolicyRegexp = regexp.MustCompile(`http[s]?\://.*/npcf-smpolicycontrol/v\d+/sm-policies/(.*)`)

func (s *npcfService) extractSMPolicyIDFromLocation(location string) string {
	match := smPolicyRegexp.FindStringSubmatch(location)
	if len(match) > 1 {
		return match[1]
	}
	// not match submatch
	return ""
}

func (s *npcfService) SendSMPolicyAssociationUpdateByUERequestModification(
	smContext *smf_context.SMContext,
	qosRules nasie.QosRules, qosFlowDescs nasie.QosFlowDescs,
) (*models.Pcf_SMPolCtrl_SmPolicyDecision, error) {
	updateSMPolicy := models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{}

	hasQoSRules := len(qosRules.Rules) > 0
	hasQoSFlowDescs := len(qosFlowDescs.Descs) > 0

	if !hasQoSRules && !hasQoSFlowDescs {
		// No UE-initiated resource request; update without RES_MO_RE.
	} else if !hasQoSRules {
		return nil, errors.New("QoS rules missing for UE-initiated request")
	} else {
		// UE SHOULD only create ONE QoS Flow in a request (TS 24.501 6.4.2.2)
		rule := qosRules.Rules[0]
		var flowDesc *nasie.QosFlowDesc
		if hasQoSFlowDescs {
			flowDesc = &qosFlowDescs.Descs[0]
		}

		var ruleOp models.Pcf_SMPolCtrl_RuleOperation
		switch rule.OpCode {
		case nasie.OpCode_CreateNewQosRule:
			ruleOp = models.Pcf_SMPolCtrl_RuleOperation_CREATE_PCC_RULE
		case nasie.OpCode_DelExistingQosRule:
			ruleOp = models.Pcf_SMPolCtrl_RuleOperation_DELETE_PCC_RULE
		case nasie.OpCode_ModifyAddPktFilters:
			ruleOp = models.Pcf_SMPolCtrl_RuleOperation_MODIFY_PCC_RULE_AND_ADD_PACKET_FILTERS
		case nasie.OpCode_ModifyDelPktFilters:
			ruleOp = models.Pcf_SMPolCtrl_RuleOperation_MODIFY_PCC_RULE_AND_DELETE_PACKET_FILTERS
		case nasie.OpCode_ModifyReplaceAllPktFilters:
			ruleOp = models.Pcf_SMPolCtrl_RuleOperation_MODIFY_PCC_RULE_AND_REPLACE_PACKET_FILTERS
		case nasie.OpCode_ModifyWoModifyingPktFilters:
			ruleOp = models.Pcf_SMPolCtrl_RuleOperation_MODIFY_PCC_RULE_WITHOUT_MODIFY_PACKET_FILTERS
		default:
			return nil, errors.New("QoS Rule Operation Unknown")
		}

		requiresFlowDesc := rule.OpCode == nasie.OpCode_CreateNewQosRule ||
			rule.OpCode == nasie.OpCode_ModifyWoModifyingPktFilters
		if requiresFlowDesc && flowDesc == nil {
			return nil, errors.New("QoS flow description required for QoS rule operation")
		}

		updateSMPolicy.RepPolicyCtrlReqTriggers = []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
			models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_RES_MO_RE,
		}

		ueInitResReq := &models.Pcf_SMPolCtrl_UeInitiatedResourceRequest{}
		ueInitResReq.RuleOp = ruleOp
		ueInitResReq.Precedence = int32(rule.Precedence)
		if flowDesc != nil {
			ueInitResReq.ReqQos = new(models.Pcf_SMPolCtrl_RequestedQos)
			ueInitResReq.ReqQos.Var5qi = int32(flowDesc.FiveQI)
			ueInitResReq.ReqQos.GbrUl = flowDesc.GFBRUplink
			ueInitResReq.ReqQos.GbrDl = flowDesc.GFBRDownlink
		}

		updateSMPolicy.UeInitResReq = ueInitResReq

		for _, pf := range rule.PktFilterList {
			if PackFiltInfo, err := s.buildPktFilterInfo(pf); err != nil {
				smContext.Log.Warning("Build PackFiltInfo failed", err)
				continue
			} else {
				updateSMPolicy.UeInitResReq.PackFiltInfo = append(updateSMPolicy.UeInitResReq.PackFiltInfo, *PackFiltInfo)
			}
		}
	}

	ctx, _, err := smf_context.GetSelf().
		GetTokenCtxForNFInstance(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
			models.Nrf_NFMgmt_NFType_PCF, smContext.SelectedPCFProfile.NfInstanceId)
	if err != nil {
		return nil, err
	}

	var client *SMPolCtrl.APIClient

	// Create SMPolicyControl Client for this SM Context
	for _, service := range smContext.SelectedPCFProfile.NfServices {
		if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL {
			client = s.getSMPolicyControlClient(service.ApiPrefix)
		}
	}

	var smPolicyDecision *models.Pcf_SMPolCtrl_SmPolicyDecision
	request := &SMPolCtrl.UpdateSMPolicyRequest{
		SmPolicyId:  &smContext.SMPolicyID,
		RequestBody: &updateSMPolicy,
	}

	smPolicyDecisionFromPCF, err := client.IndividualSMPolicyDocumentApi.UpdateSMPolicy(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("update sm policy [%s] association failed: %s", smContext.SMPolicyID, err)
	}
	smPolicyDecision = smPolicyDecisionFromPCF.Pcf_SMPolCtrl_SmPolicyDecision
	return smPolicyDecision, nil
}

func parsePortRange(portRange string) []flowdesc.PortRange {
	if portRange == "" {
		return nil
	}
	ports := strings.Split(portRange, "-")
	start, err := strconv.ParseUint(ports[0], 10, 16)
	if err != nil {
		return nil
	}
	end := start
	if len(ports) > 1 {
		if end, err = strconv.ParseUint(ports[1], 10, 16); err != nil {
			return nil
		}
	}
	return []flowdesc.PortRange{{Start: uint16(start), End: uint16(end)}}
}

func (s *npcfService) buildPktFilterInfo(pf nasie.PacketFilter) (*models.Pcf_SMPolCtrl_PacketFilterInfo, error) {
	pfInfo := &models.Pcf_SMPolCtrl_PacketFilterInfo{}

	switch pf.Dir {
	case nasie.PFD_Downlink:
		pfInfo.FlowDirection = models.Pcf_SMPolCtrl_FlowDirection_DOWNLINK
	case nasie.PFD_Uplink:
		pfInfo.FlowDirection = models.Pcf_SMPolCtrl_FlowDirection_UPLINK
	case nasie.PFD_BiDir:
		pfInfo.FlowDirection = models.Pcf_SMPolCtrl_FlowDirection_BIDIRECTIONAL
	default:
		pfInfo.FlowDirection = models.Pcf_SMPolCtrl_FlowDirection_UNSPECIFIED
	}

	const ProtocolNumberAny = 0xfc
	packetFilter := &flowdesc.IPFilterRule{
		Action: "permit",
		Dir:    "out",
		Proto:  ProtocolNumberAny,
	}

	contents := pf.Contents
	if contents.RemoteAddr != "" && contents.RemoteAddr != "any" {
		if _, remoteIPnet, err := net.ParseCIDR(contents.RemoteAddr); err == nil {
			packetFilter.Src = remoteIPnet.String()
		}
	}
	if contents.LocalAddr != "" && contents.LocalAddr != "any" && contents.LocalAddr != "assigned" {
		if _, localIPnet, err := net.ParseCIDR(contents.LocalAddr); err == nil {
			packetFilter.Dst = localIPnet.String()
		}
	}
	if contents.HavePIorNH {
		packetFilter.Proto = contents.PIorNH
	}
	if localPorts := parsePortRange(contents.LocalPortRange); localPorts != nil {
		packetFilter.DstPorts = append(packetFilter.DstPorts, localPorts...)
	}
	if remotePorts := parsePortRange(contents.RemotePortRange); remotePorts != nil {
		packetFilter.SrcPorts = append(packetFilter.SrcPorts, remotePorts...)
	}
	// SPI, TosTrafficClass and FlowLabel are already hex strings in the new IE.
	pfInfo.Spi = contents.SPI
	pfInfo.TosTrafficClass = contents.TosTrafficClass
	pfInfo.FlowLabel = contents.FlowLabel

	if desc, err := flowdesc.Encode(packetFilter); err != nil {
		return nil, err
	} else {
		pfInfo.PackFiltCont = desc
	}
	// according TS 29.212 IPFilterRule cannot use [options]
	return pfInfo, nil
}

func (s *npcfService) SendSMPolicyAssociationTermination(smContext *smf_context.SMContext) error {
	var client *SMPolCtrl.APIClient

	// Create SMPolicyControl Client for this SM Context
	for _, service := range smContext.SelectedPCFProfile.NfServices {
		if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL {
			client = s.getSMPolicyControlClient(service.ApiPrefix)
		}
	}

	if client == nil {
		return errors.Errorf("smContext not selected PCF")
	}

	ctx, _, err := smf_context.GetSelf().
		GetTokenCtxForNFInstance(models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL,
			models.Nrf_NFMgmt_NFType_PCF, smContext.SelectedPCFProfile.NfInstanceId)
	if err != nil {
		return err
	}

	request := &SMPolCtrl.DeleteSMPolicyRequest{
		SmPolicyId:  &smContext.SMPolicyID,
		RequestBody: &models.Pcf_SMPolCtrl_SmPolicyDeleteData{},
	}

	_, err = client.IndividualSMPolicyDocumentApi.DeleteSMPolicy(ctx, request)
	if err != nil {
		return fmt.Errorf("SM Policy termination failed: %v", err)
	}
	return nil
}
