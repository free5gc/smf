package context

import (
	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/smf/internal/logger"
)

type ProtocolConfigurationOptions struct {
	DNSIPv4Request     bool
	DNSIPv6Request     bool
	PCSCFIPv4Request   bool
	IPv4LinkMTURequest bool
}

func BuildGSMPDUSessionEstablishmentAccept(smContext *SMContext) ([]byte, error) {
	sessRule := smContext.SelectedSessionRule()
	authDefQos := sessRule.AuthDefQos

	accept := &message.PDUSessEstAccept{
		PDUSessId: uint8(smContext.PDUSessionID),
		PTI:       smContext.Pti,
		SelectedPDUSessType: &nasie.PDUSessType{
			Value: smContext.SelectedPDUSessionType,
		},
		SelectedSSCMode: &nasie.SSCMode{
			Mode: nasie.SSCMODE1,
		},
	}

	if v := smContext.EstAcceptCause5gSMValue; v != 0 {
		accept.Cause5GSM = &nasie.Cause5GSM{Value: v}
	}

	sessAMBR := new(nasie.SessAMBR)
	if err := sessAMBR.Set(sessRule.AuthSessAmbr.Uplink, sessRule.AuthSessAmbr.Downlink); err != nil {
		return nil, err
	}
	accept.SessAMBR = sessAMBR

	qosRules := []nasie.QosRule{
		{
			RuleId:       smContext.defRuleID,
			IsDefaultDQR: true,
			OpCode:       nasie.OpCode_CreateNewQosRule,
			Precedence:   255,
			QFI:          sessRule.DefQosQFI,
			PktFilterList: []nasie.PacketFilter{
				{
					Id:  1,
					Dir: nasie.PFD_BiDir,
					Contents: nasie.PacketFilterContents{
						MatchAll:   true,
						RemoteAddr: "any",
						LocalAddr:  "assigned",
					},
				},
			},
		},
	}

	for _, pccRule := range smContext.PCCRules {
		// QFI = 0 is ignored because it is invalid QFI
		// If real UE receives QFI = 0, it will be rejected by NAS. Then the session will be released.
		if pccRule.QFI == 0 {
			continue
		}
		if qosRule, err1 := pccRule.BuildNasQoSRule(smContext,
			nasie.OpCode_CreateNewQosRule); err1 != nil {
			logger.GsmLog.Warnln("Create QoS rule from pcc error ", err1)
		} else {
			if ruleID, err2 := smContext.QoSRuleIDGenerator.Allocate(); err2 != nil {
				return nil, err2
			} else {
				qosRule.RuleId = uint8(ruleID)
				smContext.PCCRuleIDToQoSRuleID[pccRule.PccRuleId] = uint8(ruleID)
			}
			qosRules = append(qosRules, *qosRule)
		}
	}

	accept.AuthoQosRules = &nasie.QosRules{Rules: qosRules}

	if smContext.PDUAddress != nil {
		pduAddr := new(nasie.PDUAddr)
		switch smContext.SelectedPDUSessionType {
		case nasie.PDUSessType_IPv4:
			pduAddr.IPv4 = smContext.PDUAddress.To4()
		case nasie.PDUSessType_IPv6:
			pduAddr.IPv6IfId = smContext.PDUAddress.To16()[8:16]
		case nasie.PDUSessType_IPv4v6:
			pduAddr.IPv4 = smContext.PDUAddress.To4()
			pduAddr.IPv6IfId = smContext.PDUAddress.To16()[8:16]
		}
		accept.PDUAddr = pduAddr
	}

	authDescs := []nasie.QosFlowDesc{
		{
			QFI:    sessRule.DefQosQFI,
			OpCode: nasie.QFD_Create,
			EBit:   nasie.QfdEbit_HasParamList,
			FiveQI: uint8(authDefQos.Var5qi),
		},
	}
	for _, qosFlow := range smContext.AdditionalQosFlows {
		if qosDesc, e := qosFlow.BuildNasQoSDesc(nasie.QFD_Create); e != nil {
			logger.GsmLog.Warnf("Create QoS Desc from qos flow error: %s\n", e)
		} else {
			authDescs = append(authDescs, qosDesc)
		}
	}
	accept.AuthoQosFlowDescs = &nasie.QosFlowDescs{Descs: authDescs}

	accept.SNSSAI = &nasie.SNSSAI{
		SST: uint8(smContext.SNssai.Sst),
		SD:  smContext.SNssai.Sd,
	}

	accept.DNN = &nasie.DNN{Value: smContext.Dnn}

	if smContext.ProtocolConfigurationOptions.DNSIPv4Request ||
		smContext.ProtocolConfigurationOptions.DNSIPv6Request ||
		smContext.ProtocolConfigurationOptions.PCSCFIPv4Request ||
		smContext.ProtocolConfigurationOptions.IPv4LinkMTURequest {
		fromNw := new(nasie.ExtCfgOptFromNw)

		// IPv4 DNS
		if smContext.ProtocolConfigurationOptions.DNSIPv4Request {
			fromNw.DNSIPv4Addr = smContext.DNNInfo.DNS.IPv4Addr
		}

		// IPv6 DNS
		if smContext.ProtocolConfigurationOptions.DNSIPv6Request {
			fromNw.DNSIPv6Addr = smContext.DNNInfo.DNS.IPv6Addr
		}

		// IPv4 PCSCF (need for ims DNN)
		if smContext.ProtocolConfigurationOptions.PCSCFIPv4Request {
			fromNw.P_CSCF_IPv4Addr = smContext.DNNInfo.PCSCF.IPv4Addr
		}

		// MTU
		if smContext.ProtocolConfigurationOptions.IPv4LinkMTURequest {
			fromNw.IPv4LinkMTU = 1400
		}

		accept.ExtendedProtCfgOpts = &nasie.ExtendedProtCfgOpts{FromNw: fromNw}
	}

	return accept.MarshalBinary()
}

func BuildGSMPDUSessionEstablishmentReject(smContext *SMContext, cause uint8) ([]byte, error) {
	reject := &message.PDUSessEstRej{
		PDUSessId: uint8(smContext.PDUSessionID),
		PTI:       smContext.Pti,
		Cause5GSM: &nasie.Cause5GSM{Value: cause},
	}
	return reject.MarshalBinary()
}

// BuildGSMPDUSessionReleaseCommand makes a plain NAS message.
//
// If isTriggeredByUE is true, the PTI field of the constructed NAS message is
// the value of smContext.Pti which is received from UE, otherwise it is 0.
// ref. 6.3.3.2 Network-requested PDU session release procedure initiation in TS24.501.
func BuildGSMPDUSessionReleaseCommand(smContext *SMContext, cause uint8, isTriggeredByUE bool) ([]byte, error) {
	command := &message.PDUSessRelCmd{
		PDUSessId: uint8(smContext.PDUSessionID),
		Cause5GSM: &nasie.Cause5GSM{Value: cause},
	}
	if isTriggeredByUE {
		command.PTI = smContext.Pti
	}
	return command.MarshalBinary()
}

func BuildGSMPDUSessionModificationCommand(smContext *SMContext) ([]byte, error) {
	command := &message.PDUSessModCmd{
		PDUSessId: uint8(smContext.PDUSessionID),
		PTI:       smContext.Pti,
	}
	return command.MarshalBinary()
}

func BuildGSMPDUSessionReleaseReject(smContext *SMContext) ([]byte, error) {
	reject := &message.PDUSessRelRej{
		PDUSessId: uint8(smContext.PDUSessionID),
		PTI:       smContext.Pti,
		// TODO: fix to real value
		Cause5GSM: &nasie.Cause5GSM{Value: nasie.Cause5GSM_ReqRejected},
	}
	return reject.MarshalBinary()
}

func BuildGSMPDUSessionModificationReject(smContext *SMContext) ([]byte, error) {
	reject := &message.PDUSessModRej{
		PDUSessId: uint8(smContext.PDUSessionID),
		PTI:       smContext.Pti,
		Cause5GSM: &nasie.Cause5GSM{Value: nasie.Cause5GSM_MsgTypeNonExistentOrNotImpl},
	}
	return reject.MarshalBinary()
}
