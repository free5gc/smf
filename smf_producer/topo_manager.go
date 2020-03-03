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
	AllocateDownLinkPDR(root, smContext, visited)

	for node, _ := range visited {
		visited[node] = false
	}

	AllocateDownLinkTEID(root, smContext, visited)

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
	upLink := node.DataPathToAN

	teid, err := node.UPF.GenerateTEID()

	if err != nil {
		logger.PduSessLog.Error(err)
	}

	upLink.UpLinkPDR, err = node.UPF.AddPDR()
	if err != nil {
		logger.PduSessLog.Error(err)
	}

	upLink.UpLinkPDR.Precedence = 32
	upLink.UpLinkPDR.PDI = smf_context.PDI{
		SourceInterface: pfcpType.SourceInterface{
			//Todo:
			//Have to change source interface for different upf
			InterfaceValue: pfcpType.SourceInterfaceAccess,
		},
		LocalFTeid: &pfcpType.FTEID{
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
	upLink.UpLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
	upLink.UpLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
	upLink.UpLinkPDR.State = smf_context.RULE_INITIAL

	upLink.UpLinkPDR.FAR.ApplyAction.Forw = true
	upLink.UpLinkPDR.FAR.State = smf_context.RULE_INITIAL
	upLink.UpLinkPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
		DestinationInterface: pfcpType.DestinationInterface{
			InterfaceValue: pfcpType.DestinationInterfaceCore,
		},
		NetworkInstance: []byte(smContext.Dnn),
	}

	parent := node.GetParent()
	if parent != nil {

		parentUpLinkFAR := parent.GetUpLinkFAR()

		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(teid)
		parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = node.UPF.UPIPInfo.Ipv4Address
	}

	for _, upf_link := range node.DataPathToDN {

		child := upf_link.To
		if !visited[child] {
			AllocateUpLinkPDRandTEID(child, smContext, visited)
		}

	}

}

func AllocateDownLinkPDR(node *smf_context.DataPathNode, smContext *smf_context.SMContext, visited map[*smf_context.DataPathNode]bool) {
	var err error
	var teid uint32

	if !visited[node] {
		visited[node] = true
	}

	for _, downLink := range node.DataPathToDN {

		downLink.DownLinkPDR, err = node.UPF.AddPDR()

		if err != nil {
			logger.PduSessLog.Error(err)
		}

		teid, err = node.UPF.GenerateTEID()

		if err != nil {
			logger.PduSessLog.Error(err)
		}

		downLink.DownLinkPDR.Precedence = 32
		downLink.DownLinkPDR.PDI = smf_context.PDI{
			SourceInterface: pfcpType.SourceInterface{
				//Todo:
				//Have to change source interface for different upf
				InterfaceValue: pfcpType.SourceInterfaceAccess,
			},
			LocalFTeid: &pfcpType.FTEID{
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

		downLink.DownLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
		downLink.DownLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
		downLink.DownLinkPDR.State = smf_context.RULE_INITIAL

		downLink.DownLinkPDR.FAR.ApplyAction.Forw = true
		downLink.DownLinkPDR.FAR.State = smf_context.RULE_INITIAL
		downLink.DownLinkPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
			DestinationInterface: pfcpType.DestinationInterface{
				InterfaceValue: pfcpType.DestinationInterfaceCore,
			},
			NetworkInstance: []byte(smContext.Dnn),
		}

	}

	for _, upf_link := range node.DataPathToDN {

		child := upf_link.To
		if !visited[child] {
			AllocateDownLinkPDR(child, smContext, visited)
		}

	}

	if node.IsAnchorUPF() {

		downLink := node.DLDataPathLinkForPSA
		downLink.DownLinkPDR, err = node.UPF.AddPDR()
		if err != nil {
			logger.PduSessLog.Error(err)
		}
		downLink.DownLinkPDR.Precedence = 32
		downLink.DownLinkPDR.PDI = smf_context.PDI{
			SourceInterface: pfcpType.SourceInterface{
				//Todo:
				//Have to change source interface for different upf
				InterfaceValue: pfcpType.SourceInterfaceAccess,
			},
			LocalFTeid: &pfcpType.FTEID{
				V4:          true,
				Teid:        0,
				Ipv4Address: node.UPF.UPIPInfo.Ipv4Address,
			},
			NetworkInstance: []byte(smContext.Dnn),
			UEIPAddress: &pfcpType.UEIPAddress{
				V4:          true,
				Ipv4Address: smContext.PDUAddress.To4(),
			},
		}

		downLink.DownLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
		downLink.DownLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
		downLink.DownLinkPDR.State = smf_context.RULE_INITIAL

		downLink.DownLinkPDR.FAR.ApplyAction.Forw = true
		downLink.DownLinkPDR.FAR.State = smf_context.RULE_INITIAL
		downLink.DownLinkPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
			DestinationInterface: pfcpType.DestinationInterface{
				InterfaceValue: pfcpType.DestinationInterfaceCore,
			},
			NetworkInstance: []byte(smContext.Dnn),
		}
	}
}

func AllocateDownLinkTEID(node *smf_context.DataPathNode, smContext *smf_context.SMContext, visited map[*smf_context.DataPathNode]bool) {

	if !visited[node] {
		visited[node] = true
	}

	for _, downLink := range node.DataPathToDN {

		child := downLink.To
		allocatedDownLinkTEID := downLink.DownLinkPDR.PDI.LocalFTeid.Teid

		for _, child_downLink := range child.DataPathToDN {

			childDownLinkFAR := child_downLink.DownLinkPDR.FAR
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedDownLinkTEID)
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = node.UPF.UPIPInfo.Ipv4Address
		}

	}

	for _, upf_link := range node.DataPathToDN {

		child := upf_link.To
		if !visited[child] {
			AllocateDownLinkTEID(child, smContext, visited)
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

	upLink := node.DataPathToAN
	pdr_list := []*smf_context.PDR{upLink.UpLinkPDR}
	far_list := []*smf_context.FAR{upLink.UpLinkPDR.FAR}
	bar_list := []*smf_context.BAR{}

	pfcp_message.SendPfcpSessionEstablishmentRequestForULCL(&addr, smContext, pdr_list, far_list, bar_list)

	for _, upf_link := range node.DataPathToDN {

		child := upf_link.To
		if !visited[child] {
			SendUplinkPFCPRule(upf_link.To, smContext, visited)
		}
	}

}

func SendDownLinkPFCPRule(node *smf_context.DataPathNode, smContext *smf_context.SMContext, visited map[*smf_context.DataPathNode]bool) {

	if !visited[node] {
		visited[node] = true
	}

	addr := net.UDPAddr{
		IP:   node.UPF.NodeID.NodeIdValue,
		Port: pfcpUdp.PFCP_PORT,
	}

	fmt.Println("Send to upf addr: ", addr.String())

	for _, down_link := range node.DataPathToDN {

		pdr_list := []*smf_context.PDR{down_link.DownLinkPDR}
		far_list := []*smf_context.FAR{down_link.DownLinkPDR.FAR}
		bar_list := []*smf_context.BAR{}
		pfcp_message.SendPfcpSessionModificationRequest(&addr, smContext, pdr_list, far_list, bar_list)
	}

	if node.IsAnchorUPF() {

		down_link := node.DLDataPathLinkForPSA
		pdr_list := []*smf_context.PDR{down_link.DownLinkPDR}
		far_list := []*smf_context.FAR{down_link.DownLinkPDR.FAR}
		bar_list := []*smf_context.BAR{}
		pfcp_message.SendPfcpSessionModificationRequest(&addr, smContext, pdr_list, far_list, bar_list)
	}

	for _, upf_link := range node.DataPathToDN {

		child := upf_link.To
		if !visited[child] {
			SendDownLinkPFCPRule(upf_link.To, smContext, visited)
		}
	}

}
