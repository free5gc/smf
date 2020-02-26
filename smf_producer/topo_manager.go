package smf_producer

import (
	"fmt"
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/lib/pfcp/pfcpUdp"
	"gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_pfcp/pfcp_message"
	"net"
)

func SetUpUplinkUserPlane(root *smf_context.DataPathNode, smContext *smf_context.SMContext) {

	visited := make(map[*smf_context.DataPathNode]bool)
	AllocateUpLinkPDRandTEID(root, smContext, visited)

	for node, _ := range visited {
		visited[node] = false
	}

	SendUplinkPFCPRule(root, smContext, visited)
}

func SetUpDownLinkUserPlane(root *smf_context.DataPathNode, smContext *smf_context.SMContext) {

	visited := make(map[*smf_context.DataPathNode]bool)
	AllocateDownLinkPDRandTEID(root, smContext, visited)

	for node, _ := range visited {
		visited[node] = false
	}

	SendDownLinkPFCPRule(root, smContext, visited)
}

func AllocateUpLinkPDRandTEID(node *smf_context.DataPathNode, smContext *smf_context.SMContext, visited map[*smf_context.DataPathNode]bool) {

	if !visited[node] {
		visited[node] = true
	}

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
			//Todo:
			//Have to change source interface for different upf
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

	parent := node.GetParent()
	if parent != nil {

		//parentUpLinkPDR := parent.GetUpLinkPDR()
		parentUpLinkFAR := parent.GetUpLinkFAR()

		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(teid)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = node.UPF.UPIPInfo.Ipv4Address

		// fmt.Println("In AllocateUpLinkPDRandTEID")
		// fmt.Println("My IP: ", node.UPF.NodeID.ResolveNodeIdToIp().String())
		// fmt.Println("Parent IP: ", parent.UPF.NodeID.ResolveNodeIdToIp().String())
		// fmt.Println("Parent Uplink PDR: ", parentUpLinkPDR)
		// fmt.Println("Parent Uplink PDR FAR: ", parentUpLinkPDR.FAR)
		// fmt.Println("Parent Uplink PDR FAR addr: ", &parentUpLinkPDR.FAR)
		// fmt.Println("Parent UpLink FAR: ", parentUpLinkFAR)
		// fmt.Println("Parent UpLink FAR address: ", &parentUpLinkFAR)
	}

	for _, upf_link := range node.Next {

		child := upf_link.To
		if !visited[child] {
			AllocateUpLinkPDRandTEID(child, smContext, visited)
		}

	}

}

func AllocateDownLinkPDRandTEID(node *smf_context.DataPathNode, smContext *smf_context.SMContext, visited map[*smf_context.DataPathNode]bool) {
	var err error
	var teid uint32

	if !visited[node] {
		visited[node] = true
	}

	for _, downLink := range node.Next {

		downLink.PDR, err = node.UPF.AddPDR()
		if err != nil {
			logger.PduSessLog.Error(err)
		}
		downLink.PDR.Precedence = 32
		downLink.PDR.PDI = smf_context.PDI{
			SourceInterface: pfcpType.SourceInterface{
				//Todo:
				//Have to change source interface for different upf
				InterfaceValue: pfcpType.SourceInterfaceAccess,
			},
			NetworkInstance: []byte(smContext.Dnn),
			UEIPAddress: &pfcpType.UEIPAddress{
				V4:          true,
				Ipv4Address: smContext.PDUAddress.To4(),
			},
		}

		if !node.IsAnchorUPF() {
			teid, err = node.UPF.GenerateTEID()

			if err != nil {
				logger.PduSessLog.Error(err)
			}

			downLink.PDR.PDI.LocalFTeid = pfcpType.FTEID{
				V4:          true,
				Teid:        teid,
				Ipv4Address: node.UPF.UPIPInfo.Ipv4Address,
			}
		}

		downLink.PDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
		downLink.PDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
		downLink.PDR.State = smf_context.RULE_INITIAL

		downLink.PDR.FAR.ApplyAction.Forw = true
		downLink.PDR.FAR.State = smf_context.RULE_INITIAL
		downLink.PDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
			DestinationInterface: pfcpType.DestinationInterface{
				InterfaceValue: pfcpType.DestinationInterfaceCore,
			},
			NetworkInstance: []byte(smContext.Dnn),
		}

	}

	parent := node.GetParent()
	if parent != nil {

		parentUpLinkFAR := parent.GetUpLinkFAR()

		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(teid)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = node.UPF.UPIPInfo.Ipv4Address

	}

	for _, upf_link := range node.Next {

		child := upf_link.To
		if !visited[child] {
			AllocateUpLinkPDRandTEID(child, smContext, visited)
		}

	}
}

func SendUplinkPFCPRule(node *smf_context.DataPathNode, smContext *smf_context.SMContext, visited map[*smf_context.DataPathNode]bool) {

	if !visited[node] {
		visited[node] = true
	}

	addr := net.UDPAddr{
		IP:   node.UPF.NodeID.NodeIdValue,
		Port: pfcpUdp.PFCP_PORT,
	}

	fmt.Println("Send to upf addr: ", addr.String())

	upLink := node.Prev
	pdr_list := []*smf_context.PDR{upLink.PDR}
	far_list := []*smf_context.FAR{upLink.PDR.FAR}
	bar_list := []*smf_context.BAR{}

	//parent := node.GetParent()

	// if parent != nil {
	// 	fmt.Println("My IP: ", node.UPF.NodeID.ResolveNodeIdToIp().String())
	// 	fmt.Println("Parent IP: ", parent.UPF.NodeID.ResolveNodeIdToIp().String())
	// 	fmt.Println("Parent UpLink FAR: ", upLink.PDR.FAR)
	// }

	pfcp_message.SendPfcpSessionEstablishmentRequestForULCL(&addr, smContext, pdr_list, far_list, bar_list)

	for _, upf_link := range node.Next {

		child := upf_link.To
		if !visited[child] {
			SendUplinkPFCPRule(upf_link.To, smContext, visited)
		}
	}
}

func SendDownLinkPFCPRule(node *smf_context.DataPathNode, smContext *smf_context.SMContext, visited map[*smf_context.DataPathNode]bool) {

}
