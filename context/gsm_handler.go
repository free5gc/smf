package context

import (
	"free5gc/lib/nas/nasConvert"
	"free5gc/lib/nas/nasMessage"
	"free5gc/src/smf/logger"
)

func (smContext *SMContext) HandlePDUSessionEstablishmentRequest(req *nasMessage.PDUSessionEstablishmentRequest) {
	// Retrieve PDUSessionID
	smContext.PDUSessionID = int32(req.PDUSessionID.GetPDUSessionID())
	logger.GsmLog.Infoln("In HandlePDUSessionEstablishmentRequest")
	// Handle PDUSessionType
	if req.PDUSessionType != nil {
		requestedPDUSessionType := req.PDUSessionType.GetPDUSessionTypeValue()
		if smContext.isAllowedPDUSessionType(requestedPDUSessionType) {
			smContext.SelectedPDUSessionType = requestedPDUSessionType
		} else {
			logger.CtxLog.Errorf("requested pdu session type [%s] is not in allowed type\n", nasConvert.PDUSessionTypeToModels(requestedPDUSessionType))
		}
	} else {
		// Default to IPv4
		// TODO: use Default PDU Session Type
		smContext.SelectedPDUSessionType = nasMessage.PDUSessionTypeIPv4
	}

	if req.ExtendedProtocolConfigurationOptions != nil {
		EPCOContents := req.ExtendedProtocolConfigurationOptions.GetExtendedProtocolConfigurationOptionsContents()
		protocolConfigurationOptions := nasConvert.NewProtocolConfigurationOptions()
		protocolConfigurationOptions.UnMarshal(EPCOContents)
		logger.GsmLog.Infoln("Protocol Configuration Options")
		logger.GsmLog.Infoln(protocolConfigurationOptions)

		for _, container := range protocolConfigurationOptions.ProtocolOrContainerList {
			logger.GsmLog.Traceln("Container ID: ", container.ProtocolOrContainerID)
			logger.GsmLog.Traceln("Container Length: ", container.LengthofContents)
			switch container.ProtocolOrContainerID {
			case nasMessage.PCSCFIPv6AddressRequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type PCSCFIPv6AddressRequestUL")
			case nasMessage.IMCNSubsystemSignalingFlagUL:
				logger.GsmLog.Infoln("Didn't Implement container type IMCNSubsystemSignalingFlagUL")
			case nasMessage.DNSServerIPv6AddressRequestUL:
				smContext.ProtocolConfigurationOptions.DNSIPv6Request = true
			case nasMessage.NotSupportedUL:
				logger.GsmLog.Infoln("Didn't Implement container type NotSupportedUL")
			case nasMessage.MSSupportOfNetworkRequestedBearerControlIndicatorUL:
				logger.GsmLog.Infoln("Didn't Implement container type MSSupportOfNetworkRequestedBearerControlIndicatorUL")
			case nasMessage.DSMIPv6HomeAgentAddressRequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type DSMIPv6HomeAgentAddressRequestUL")
			case nasMessage.DSMIPv6HomeNetworkPrefixRequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type DSMIPv6HomeNetworkPrefixRequestUL")
			case nasMessage.DSMIPv6IPv4HomeAgentAddressRequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type DSMIPv6IPv4HomeAgentAddressRequestUL")
			case nasMessage.IPAddressAllocationViaNASSignallingUL:
				logger.GsmLog.Infoln("Didn't Implement container type IPAddressAllocationViaNASSignallingUL")
			case nasMessage.IPv4AddressAllocationViaDHCPv4UL:
				logger.GsmLog.Infoln("Didn't Implement container type IPv4AddressAllocationViaDHCPv4UL")
			case nasMessage.PCSCFIPv4AddressRequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type PCSCFIPv4AddressRequestUL")
			case nasMessage.DNSServerIPv4AddressRequestUL:
				smContext.ProtocolConfigurationOptions.DNSIPv4Request = true
			case nasMessage.MSISDNRequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type MSISDNRequestUL")
			case nasMessage.IFOMSupportRequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type IFOMSupportRequestUL")
			case nasMessage.IPv4LinkMTURequestUL:
				logger.GsmLog.Infoln("Didn't Implenment container type IPv4LinkMTURequestUL")
			case nasMessage.MSSupportOfLocalAddressInTFTIndicatorUL:
				logger.GsmLog.Infoln("Didn't Implement container type MSSupportOfLocalAddressInTFTIndicatorUL")
			case nasMessage.PCSCFReSelectionSupportUL:
				logger.GsmLog.Infoln("Didn't Implement container type PCSCFReSelectionSupportUL")
			case nasMessage.NBIFOMRequestIndicatorUL:
				logger.GsmLog.Infoln("Didn't Implement container type NBIFOMRequestIndicatorUL")
			case nasMessage.NBIFOMModeUL:
				logger.GsmLog.Infoln("Didn't Implement container type NBIFOMModeUL")
			case nasMessage.NonIPLinkMTURequestUL:
				logger.GsmLog.Infoln("Didn't Implement container type NonIPLinkMTURequestUL")
			case nasMessage.APNRateControlSupportIndicatorUL:
				logger.GsmLog.Infoln("Didn't Implement container type APNRateControlSupportIndicatorUL")
			case nasMessage.UEStatus3GPPPSDataOffUL:
				logger.GsmLog.Infoln("Didn't Implement container type UEStatus3GPPPSDataOffUL")
			case nasMessage.ReliableDataServiceRequestIndicatorUL:
				logger.GsmLog.Infoln("Didn't Implement container type ReliableDataServiceRequestIndicatorUL")
			case nasMessage.AdditionalAPNRateControlForExceptionDataSupportIndicatorUL:
				logger.GsmLog.Infoln("Didn't Implement container type AdditionalAPNRateControlForExceptionDataSupportIndicatorUL")
			case nasMessage.PDUSessionIDUL:
				logger.GsmLog.Infoln("Didn't Implement container type PDUSessionIDUL")
			default:
				logger.GsmLog.Infoln("Unknown Container ID [%d]", container.ProtocolOrContainerID)
			}
		}
	}

	smContext.PDUAddress = AllocUEIP()
}

func (smContext *SMContext) HandlePDUSessionReleaseRequest(req *nasMessage.PDUSessionReleaseRequest) {
	logger.GsmLog.Infof("Handle Pdu Session Release Request")
}
