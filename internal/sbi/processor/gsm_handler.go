package processor

import (
	"fmt"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

type GSMError struct {
	GSMCause uint8
}

var _ error = &GSMError{}

func (e *GSMError) Error() string {
	return fmt.Sprintf("gsm error cause[%d]", e.GSMCause)
}

func HandlePDUSessionEstablishmentRequest(
	smCtx *smf_context.SMContext, req *message.PDUSessEstReq,
) error {
	// Retrieve PDUSessionID
	smCtx.PDUSessionID = int32(req.PDUSessionID())
	logger.GsmLog.Infoln("In HandlePDUSessionEstablishmentRequest")

	// Retrieve PTI (Procedure transaction identity)
	smCtx.Pti = req.ProcedureTransactionID()

	// Retrieve MaxIntegrityProtectedDataRate of UE for UP Security
	if ipmdr := req.IntegrityProtectionMaxDataRate; ipmdr != nil {
		switch ipmdr.Uplink {
		case 0x00:
			smCtx.MaximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink = models.
				Smf_PDUSess_MaxIntegrityProtectedDataRate_64_KBPS
		case 0xff:
			smCtx.MaximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink = models.
				Smf_PDUSess_MaxIntegrityProtectedDataRate_MAX_UE_RATE
		}
		switch ipmdr.Downlink {
		case 0x00:
			smCtx.MaximumDataRatePerUEForUserPlaneIntegrityProtectionForDownLink = models.
				Smf_PDUSess_MaxIntegrityProtectedDataRate_64_KBPS
		case 0xff:
			smCtx.MaximumDataRatePerUEForUserPlaneIntegrityProtectionForDownLink = models.
				Smf_PDUSess_MaxIntegrityProtectedDataRate_MAX_UE_RATE
		}
	}
	// Handle PDUSessionType
	if req.PDUSessType != nil {
		requestedPDUSessionType := req.PDUSessType.Value
		if err := smCtx.IsAllowedPDUSessionType(requestedPDUSessionType); err != nil {
			logger.CtxLog.Errorf("%s", err)
			return &GSMError{
				GSMCause: nasie.Cause5GSM_PDUSessTypeIpv4OnlyAllowed,
			}
		}
	} else {
		// Set to default supported PDU Session Type
		switch smf_context.GetSelf().SupportedPDUSessionType {
		case "IPv4":
			smCtx.SelectedPDUSessionType = nasie.PDUSessType_IPv4
		case "IPv6":
			smCtx.SelectedPDUSessionType = nasie.PDUSessType_IPv6
		case "IPv4v6":
			smCtx.SelectedPDUSessionType = nasie.PDUSessType_IPv4v6
		case "Ethernet":
			smCtx.SelectedPDUSessionType = nasie.PDUSessType_Ethernet
		default:
			smCtx.SelectedPDUSessionType = nasie.PDUSessType_IPv4
		}
	}

	if req.ExtendedProtCfgOpts != nil && req.ExtendedProtCfgOpts.FromMs != nil {
		fromMs := req.ExtendedProtCfgOpts.FromMs
		logger.GsmLog.Infoln("Protocol Configuration Options")
		logger.GsmLog.Infof("%+v", fromMs)

		if fromMs.DNSV6Req {
			smCtx.ProtocolConfigurationOptions.DNSIPv6Request = true
		}
		if fromMs.DNSV4Req {
			smCtx.ProtocolConfigurationOptions.DNSIPv4Request = true
		}
		if fromMs.P_CSCF_IPv4AddrReq {
			smCtx.ProtocolConfigurationOptions.PCSCFIPv4Request = true
		}
		if fromMs.IPv4LinkMTUReq {
			smCtx.ProtocolConfigurationOptions.IPv4LinkMTURequest = true
		}
	}
	return nil
}

func HandlePDUSessionReleaseRequest(
	smCtx *smf_context.SMContext, req *message.PDUSessRelReq,
) {
	logger.GsmLog.Infof("Handle Pdu Session Release Request")

	// Retrieve PTI (Procedure transaction identity)
	smCtx.Pti = req.ProcedureTransactionID()
}

func (p *Processor) HandlePDUSessionModificationRequest(
	smCtx *smf_context.SMContext, req *message.PDUSessModReq,
) (*message.PDUSessModCmd, error) {
	logger.GsmLog.Infof("Handle Pdu Session Modification Request")

	// Retrieve PTI (Procedure transaction identity)
	smCtx.Pti = req.ProcedureTransactionID()

	rsp := &message.PDUSessModCmd{
		PDUSessId: uint8(smCtx.PDUSessionID),
		PTI:       smCtx.Pti,
	}

	reqQoSRules := nasie.QosRules{}
	reqQoSFlowDescs := nasie.QosFlowDescs{}

	if req.ReqQosRules != nil {
		reqQoSRules = *req.ReqQosRules
	}
	if req.ReqQosFlowDescs != nil {
		reqQoSFlowDescs = *req.ReqQosFlowDescs
	}

	prevPccRules := make(map[string]*smf_context.PCCRule, len(smCtx.PCCRules))
	for id, rule := range smCtx.PCCRules {
		prevPccRules[id] = rule
	}
	prevQoSRuleIDs := make(map[string]uint8, len(smCtx.PCCRuleIDToQoSRuleID))
	for id, ruleID := range smCtx.PCCRuleIDToQoSRuleID {
		prevQoSRuleIDs[id] = ruleID
	}

	smPolicyDecision, err_ := p.Consumer().SendSMPolicyAssociationUpdateByUERequestModification(
		smCtx, reqQoSRules, reqQoSFlowDescs)
	if err_ != nil {
		return nil, fmt.Errorf("sm policy update failed: %w", err_)
	}
	if smPolicyDecision == nil {
		smPolicyDecision = &models.Pcf_SMPolCtrl_SmPolicyDecision{PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{}}
	}
	if smPolicyDecision.PccRules == nil {
		smPolicyDecision.PccRules = map[string]*models.Pcf_SMPolCtrl_PccRule{}
	}

	// Update SessionRule from decision
	if errApplySessionRules := smCtx.ApplySessionRules(smPolicyDecision); errApplySessionRules != nil {
		return nil, fmt.Errorf("PDUSessionSMContextCreate err: %v", errApplySessionRules)
	}

	if errApplyPccRules := smCtx.ApplyPccRules(smPolicyDecision); errApplyPccRules != nil {
		smCtx.Log.Errorf("apply sm policy decision error: %+v", errApplyPccRules)
	}

	authQoSRules := []nasie.QosRule{}
	authQoSFlowDesc := reqQoSFlowDescs

	for id, pccModel := range smPolicyDecision.PccRules {
		pccRule := smCtx.PCCRules[id]
		if pccRule == nil {
			pccRule = prevPccRules[id]
		}
		if pccRule == nil {
			smCtx.Log.Warnf("skip QoS rule build for unknown PCCRule[%s]", id)
			continue
		}

		opCode, opCodeFromReq := nasie.QosRuleOpCode(0), false
		if qosRuleID, ok := prevQoSRuleIDs[id]; ok {
			for _, reqRule := range reqQoSRules.Rules {
				if reqRule.RuleId == qosRuleID {
					opCode = reqRule.OpCode
					opCodeFromReq = true
					break
				}
			}
		}
		if pccModel == nil {
			opCode = nasie.OpCode_DelExistingQosRule
			opCodeFromReq = true
		}
		if !opCodeFromReq {
			if _, existed := prevPccRules[id]; existed {
				opCode = nasie.OpCode_ModifyReplaceAllPktFilters
			} else {
				opCode = nasie.OpCode_CreateNewQosRule
			}
		} else {
			switch opCode {
			case nasie.OpCode_CreateNewQosRule,
				nasie.OpCode_DelExistingQosRule,
				nasie.OpCode_ModifyReplaceAllPktFilters,
				nasie.OpCode_ModifyWoModifyingPktFilters:
			default:
				opCode = nasie.OpCode_ModifyReplaceAllPktFilters
			}
		}

		ruleID, hasRuleID := prevQoSRuleIDs[id]
		if !hasRuleID {
			if opCode != nasie.OpCode_CreateNewQosRule {
				return nil, fmt.Errorf("missing QoS rule id for PCCRule[%s]", id)
			}
			if smCtx.QoSRuleIDGenerator == nil {
				return nil, fmt.Errorf("QoS rule id generator not initialized")
			}
			newRuleID, err := smCtx.QoSRuleIDGenerator.Allocate()
			if err != nil {
				return nil, err
			}
			ruleID = uint8(newRuleID)
			if smCtx.PCCRuleIDToQoSRuleID == nil {
				smCtx.PCCRuleIDToQoSRuleID = make(map[string]uint8)
			}
			smCtx.PCCRuleIDToQoSRuleID[id] = ruleID
		}

		// build nas QoS Rule
		rule, err := pccRule.BuildNasQoSRule(smCtx, opCode)
		if err != nil {
			return nil, err
		}
		rule.RuleId = ruleID

		authQoSRules = append(authQoSRules, *rule)
	}

	if len(authQoSRules) > 0 {
		rsp.AuthoQosRules = &nasie.QosRules{Rules: authQoSRules}
	}

	if len(authQoSFlowDesc.Descs) > 0 {
		rsp.AuthoQosFlowDescs = &authQoSFlowDesc
	}

	// TS 23.502 4.3.3.2 step 2a: update the UPF(s) with the N4 rules of the new,
	// modified or removed QoS flows before the PDU Session Modification Command
	// is sent, in the same sequence as HandleSMPolicyUpdateNotify uses for a
	// PCF-initiated change. ActivateUPFSession sends every pending PDR
	// (additional packet filter PDRs included), FAR, QER and URR to the UPF
	// that owns it. As on that path, a UPF refusal does not turn into a Reject:
	// the PCF decision is already applied to the SM context and cannot be
	// rolled back here, so a Reject would leave the UE and the SMF out of step.
	smCtx.SendUpPathChgNotification("EARLY", SendUpPathChgEventExposureNotification)
	p.ActivateUPFSession(smCtx, nil)
	smCtx.SendUpPathChgNotification("LATE", SendUpPathChgEventExposureNotification)
	smCtx.PostRemoveDataPath()

	return rsp, nil
}
