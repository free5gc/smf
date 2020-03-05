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
		return
	}

	upLink.UpLinkPDR, err = node.UPF.AddPDR()
	if err != nil {
		logger.PduSessLog.Error(err)
		return
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
		if !visited[child] && child.InUse {
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
		if !visited[child] && child.InUse {
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
		if !visited[child] && child.InUse {
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

	// fmt.Println("Send to upf addr: ", addr.String())
	// fmt.Println("Send to uplink pdr: ", upLink.PDR)
	// fmt.Println("Send to uplink far: ", upLink.PDR.FAR)

	pfcp_message.SendPfcpSessionEstablishmentRequestForULCL(&addr, smContext, pdr_list, far_list, bar_list)

	for _, upf_link := range node.DataPathToDN {

		child := upf_link.To
		if !visited[child] && child.InUse {
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
		if !visited[child] && child.InUse {
			SendDownLinkPFCPRule(upf_link.To, smContext, visited)
		}
	}

}

func SetUPPSA2Path(smContext *smf_context.SMContext, psa2_path_after_ulcl []*smf_context.UPNode, start_node *smf_context.DataPathNode) {

	lowerBound := 0
	upperBound := len(psa2_path_after_ulcl) - 1
	curDataPathNode := start_node
	var downLink *smf_context.DataPathLink

	//Allocate upLink and downLink PDR
	for idx, _ := range psa2_path_after_ulcl {

		upLinkPDR := curDataPathNode.GetUpLinkPDR()

		teid, err := curDataPathNode.UPF.GenerateTEID()

		if err != nil {
			logger.PduSessLog.Error(err)
		}

		upLinkPDR, err = curDataPathNode.UPF.AddPDR()
		if err != nil {
			logger.PduSessLog.Error(err)
		}

		upLinkPDR.Precedence = 32
		upLinkPDR.PDI = smf_context.PDI{
			SourceInterface: pfcpType.SourceInterface{
				//Todo:
				//Have to change source interface for different upf
				InterfaceValue: pfcpType.SourceInterfaceAccess,
			},
			LocalFTeid: &pfcpType.FTEID{
				V4:          true,
				Teid:        teid,
				Ipv4Address: curDataPathNode.UPF.UPIPInfo.Ipv4Address,
			},
			NetworkInstance: []byte(smContext.Dnn),
			UEIPAddress: &pfcpType.UEIPAddress{
				V4:          true,
				Ipv4Address: smContext.PDUAddress.To4(),
			},
		}
		upLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
		upLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
		upLinkPDR.State = smf_context.RULE_INITIAL

		upLinkPDR.FAR.ApplyAction.Forw = true
		upLinkPDR.FAR.State = smf_context.RULE_INITIAL
		upLinkPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
			DestinationInterface: pfcpType.DestinationInterface{
				InterfaceValue: pfcpType.DestinationInterfaceCore,
			},
			NetworkInstance: []byte(smContext.Dnn),
		}

		if curDataPathNode.IsAnchorUPF() {

			downLink = curDataPathNode.DLDataPathLinkForPSA
		} else {
			nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
			downLink = curDataPathNode.Next[nextUPFID]
		}

		downLink.PDR, err = curDataPathNode.UPF.AddPDR()

		if err != nil {
			logger.PduSessLog.Error(err)
		}

		teid, err = curDataPathNode.UPF.GenerateTEID()

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
			LocalFTeid: &pfcpType.FTEID{
				V4:          true,
				Teid:        teid,
				Ipv4Address: curDataPathNode.UPF.UPIPInfo.Ipv4Address,
			},
			NetworkInstance: []byte(smContext.Dnn),
			UEIPAddress: &pfcpType.UEIPAddress{
				V4:          true,
				Ipv4Address: smContext.PDUAddress.To4(),
			},
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

		if idx != upperBound {
			curDataPathNode = downLink.To
		}

	}

	curDataPathNode = start_node
	//Allocate upLink and downLink TEID
	for idx, _ := range psa2_path_after_ulcl {

		switch idx {
		case lowerBound:

			if !curDataPathNode.IsANUPF() {
				nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
				downLink = curDataPathNode.Next[nextUPFID]
				allocatedDownLinkTEID := downLink.PDR.PDI.LocalFTeid.Teid
				child := downLink.To

				var childDownLinkFAR *smf_context.FAR

				if child.IsANUPF() {

					childDownLinkFAR = child.DLDataPathLinkForPSA.PDR.FAR
				} else {

					nextNextUPFID := psa2_path_after_ulcl[idx+2].UPF.GetUPFID()
					childDownLinkFAR = child.Next[nextNextUPFID].PDR.FAR
				}
				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedDownLinkTEID)
				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address

			}

		case upperBound:

			parent := curDataPathNode.GetParent()
			if parent != nil {
				allocatedUPLinkTEID := curDataPathNode.Prev.PDR.PDI.LocalFTeid.Teid
				parentUpLinkFAR := parent.GetUpLinkFAR()

				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedUPLinkTEID)
				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address
			}
		default:

			nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
			downLink = curDataPathNode.Next[nextUPFID]
			allocatedDownLinkTEID := downLink.PDR.PDI.LocalFTeid.Teid
			child := downLink.To

			var childDownLinkFAR *smf_context.FAR

			if child.IsANUPF() {

				childDownLinkFAR = child.DLDataPathLinkForPSA.PDR.FAR
			} else {

				nextNextUPFID := psa2_path_after_ulcl[idx+2].UPF.GetUPFID()
				childDownLinkFAR = child.Next[nextNextUPFID].PDR.FAR
			}
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedDownLinkTEID)
			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address

			parent := curDataPathNode.GetParent()
			if parent != nil {
				allocatedUPLinkTEID := curDataPathNode.Prev.PDR.PDI.LocalFTeid.Teid
				parentUpLinkFAR := parent.GetUpLinkFAR()

				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedUPLinkTEID)
				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address
			}

		}

		if idx != upperBound {
			curDataPathNode = downLink.To
		}
	}

	curDataPathNode = start_node
	for idx, _ := range psa2_path_after_ulcl {

		addr := net.UDPAddr{
			IP:   curDataPathNode.UPF.NodeID.NodeIdValue,
			Port: pfcpUdp.PFCP_PORT,
		}

		fmt.Println("Send to upf addr: ", addr.String())

		upLink := curDataPathNode.Prev

		if curDataPathNode.IsANUPF() {

			downLink = curDataPathNode.DLDataPathLinkForPSA
		} else {

			nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
			downLink = curDataPathNode.Next[nextUPFID]
		}

		pdr_list := []*smf_context.PDR{upLink.PDR, downLink.PDR}
		far_list := []*smf_context.FAR{upLink.PDR.FAR, downLink.PDR.FAR}
		bar_list := []*smf_context.BAR{}

		pfcp_message.SendPfcpSessionEstablishmentRequestForULCL(&addr, smContext, pdr_list, far_list, bar_list)
	}

}
