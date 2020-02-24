package smf_producer

import (
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/lib/pfcp/pfcpUdp"
	"gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_pfcp/pfcp_message"
	"net"
)

func SetUpUplinkUserPlane(root *smf_context.DataPathNode, smContext *smf_context.SMContext) {

	AllocateUpLinkPDRandTEID(root, smContext)
	SendUplinkPFCPRule(root, smContext)
}

func AllocateUpLinkPDRandTEID(node *smf_context.DataPathNode, smContext *smf_context.SMContext) {

	var err error

	upLink := node.Prev

	teid, err := node.UPF.GenerateTEID()

	if err != nil {
		logger.PduSessLog.Error(err)
	}

	upLink.PDR, err = node.UPF.AddPDR()
	if err != nil {
		logger.PduSessLog.Error(err)
	}

	upLink.PDR.Precedence = 32
	upLink.PDR.PDI = smf_context.PDI{
		SourceInterface: pfcpType.SourceInterface{
			InterfaceValue: pfcpType.SourceInterfaceAccess,
		},
		LocalFTeid: pfcpType.FTEID{
			V4:          true,
			Teid:        teid,
			Ipv4Address: node.UPF.UPIPInfo.Ipv4Address,
		},
		NetworkInstance: []byte(smContext.Dnn),
		UEIPAddress: &pfcpType.UEIPAddress{
			V4:          true,
			Ipv4Address: smContext.PDUAddress.To4(),
		},
	}
	upLink.PDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
	upLink.PDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
	upLink.PDR.State = smf_context.RULE_INITIAL

	upLink.PDR.FAR.ApplyAction.Forw = true
	upLink.PDR.FAR.State = smf_context.RULE_INITIAL
	upLink.PDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
		DestinationInterface: pfcpType.DestinationInterface{
			InterfaceValue: pfcpType.DestinationInterfaceCore,
		},
		NetworkInstance: []byte(smContext.Dnn),
	}

	parent := node.Prev.To
	if parent != nil {

		parentUpLinkFAR := parent.Prev.PDR.FAR

		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(teid)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = node.UPF.UPIPInfo.Ipv4Address

	}

	for _, upf_link := range node.Next {

		AllocateUpLinkPDRandTEID(upf_link.To, smContext)
	}

}

func SendUplinkPFCPRule(node *smf_context.DataPathNode, smContext *smf_context.SMContext) {

	addr := net.UDPAddr{
		IP:   node.UPF.NodeID.NodeIdValue,
		Port: pfcpUdp.PFCP_PORT,
	}

	upLink := node.Prev
	pdr_list := []*smf_context.PDR{upLink.PDR}
	far_list := []*smf_context.FAR{upLink.PDR.FAR}
	bar_list := []*smf_context.BAR{}

	pfcp_message.SendPfcpSessionEstablishmentRequestForULCL(&addr, smContext, pdr_list, far_list, bar_list)
}
