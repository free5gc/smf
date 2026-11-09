package pfcp

import (
	"slices"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// Only grouped IEs need admission before go-pfcp decodes them recursively.
// Leaf and opaque vendor/unknown IEs retain the existing decoder handling.
// Scopes follow TS 29.244 V16.7.0 clauses 7.4/7.5, with the corresponding
// V17.10.0 optional groups. Unrecognized groups are skipped by wire length
// (7.6.9), never traversed. Update these scopes when adding a grouped feature.
var messageGroupedScopes = map[uint8][]ie.IEType{
	message.MsgTypeAssociationSetupRequest: {
		ie.PFCPSessionRetentionInformation, ie.UEIPAddressPoolInformation,
		ie.GTPUPathQoSControlInformation, ie.ClockDriftControlInformation,
	},
	message.MsgTypeAssociationSetupResponse: {
		ie.UEIPAddressPoolInformation, ie.GTPUPathQoSControlInformation, ie.ClockDriftControlInformation,
	},
	message.MsgTypeAssociationUpdateRequest: {
		ie.ClockDriftControlInformation, ie.UEIPAddressPoolInformation,
		ie.GTPUPathQoSControlInformation, ie.UEIPAddressUsageInformation,
	},
	message.MsgTypeAssociationUpdateResponse: {ie.UEIPAddressUsageInformation},
	message.MsgTypeNodeReportRequest: {
		ie.UserPlanePathFailureReport, ie.UserPlanePathRecoveryReport, ie.ClockDriftReport, ie.GTPUPathQoSReport,
	},
	message.MsgTypeSessionEstablishmentResponse: {
		ie.CreatedPDR, ie.LoadControlInformation, ie.OverloadControlInformation,
		ie.CreatedTrafficEndpoint, ie.CreatedBridgeInfoForTSC, ie.ATSSSControlParameters,
	},
	message.MsgTypeSessionModificationResponse: {
		ie.CreatedPDR, ie.LoadControlInformation, ie.OverloadControlInformation,
		ie.UsageReportWithinSessionModificationResponse, ie.CreatedTrafficEndpoint,
		ie.CreatedBridgeInfoForTSC, ie.ATSSSControlParameters, ie.UpdatedPDR,
		ie.PacketRateStatusReportWithinSessionModificationResponse,
		ie.TSCManagementInformationWithinSessionModificationResponse,
	},
	message.MsgTypeSessionDeletionResponse: {
		ie.LoadControlInformation, ie.OverloadControlInformation, ie.UsageReportWithinSessionDeletionResponse,
	},
	message.MsgTypeSessionReportRequest: {
		ie.DownlinkDataReport, ie.UsageReportWithinSessionReportRequest, ie.ErrorIndicationReport,
		ie.LoadControlInformation, ie.OverloadControlInformation, ie.PacketRateStatusReport,
		ie.TSCManagementInformationWithinSessionReportRequest, ie.SessionReport,
	},
}

// These are the grouped-to-grouped edges in the admitted inbound scopes.
// All other supported children of these groups are leaves. The graph is
// acyclic; its longest path is two groups (including the message-level IE).
// References: 7.4.5.1.5, 7.5.3.7, 7.5.5.2, 7.5.7.2, 7.5.8.3 and 7.5.8.6.
var nestedGroupedScopes = map[ie.IEType][]ie.IEType{
	ie.GTPUPathQoSReport:                            {ie.QoSInformationInGTPUPathQoSReport},
	ie.ATSSSControlParameters:                       {ie.MPTCPParameters, ie.ATSSSLLParameters, ie.PMFParameters},
	ie.UsageReportWithinSessionModificationResponse: {ie.EthernetTrafficInformation},
	ie.UsageReportWithinSessionDeletionResponse:     {ie.EthernetTrafficInformation},
	ie.UsageReportWithinSessionReportRequest: {
		ie.ApplicationDetectionInformation, ie.EthernetTrafficInformation,
		ie.JoinIPMulticastInformationWithinUsageReport, ie.LeaveIPMulticastInformationWithinUsageReport,
	},
	ie.SessionReport: {ie.AccessAvailabilityReport, ie.QoSMonitoringReport},
}

func groupedIEAllowed(messageType uint8, parent, child ie.IEType) bool {
	if parent == 0 {
		return slices.Contains(messageGroupedScopes[messageType], child)
	}
	return slices.Contains(nestedGroupedScopes[parent], child)
}
