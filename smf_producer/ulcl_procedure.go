package smf_producer

import (
	"fmt"
	"gofree5gc/lib/flowdesc"
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/lib/pfcp/pfcpUdp"
	"gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_pfcp/pfcp_message"
	"net"
)

func AddPDUSessionAnchorAndULCL(smContext *smf_context.SMContext) {

	bpManager := smContext.BPManager
	upfRoot := smContext.Tunnel.UpfRoot
	//select PSA2
	bpManager.SelectPSA2()
	err := upfRoot.EnableUserPlanePath(bpManager.PSA2Path)
	if err != nil {
		logger.PduSessLog.Errorln(err)
		return
	}
	//select an upf as ULCL
	err = bpManager.FindULCL(smContext)
	if err != nil {
		logger.PduSessLog.Errorln(err)
		return
	}

	//Establish PSA2
	EstablishPSA2(smContext)
	//Establish ULCL
	EstablishULCL(smContext)

	//updatePSA1 downlink
	//UpdatePSA1DownLink(smContext)
	//updatePSA2 downlink
	UpdatePSA2DownLink(smContext)
	//update AN for new CN Info

}

func EstablishPSA2(smContext *smf_context.SMContext) {

	//upfRoot := smContext.Tunnel.UpfRoot
	bpMGR := smContext.BPManager
	psa2_path := bpMGR.PSA2Path

	curDataPathNode := bpMGR.ULCLDataPathNode
	upperBound := len(psa2_path) - 1

	if bpMGR.ULCLState == smf_context.IsOnlyULCL {
		for idx := bpMGR.ULCLIdx; idx <= upperBound; idx++ {

			if idx == bpMGR.ULCLIdx {

				nextUPFID := psa2_path[idx+1].UPF.GetUPFID()
				curDataPathNode = curDataPathNode.DataPathToDN[nextUPFID].To
			} else {

				SetUPPSA2Path(smContext, psa2_path[idx:], curDataPathNode)
				break
			}
		}
	}

	logger.PduSessLog.Traceln("End of EstablishPSA2")

	return
}

func EstablishULCL(smContext *smf_context.SMContext) {

	logger.PduSessLog.Traceln("In EstablishULCL")

	bpMGR := smContext.BPManager
	ulcl := bpMGR.ULCLDataPathNode

	if ulcl.IsAnchorUPF() {
		return
	}

	if bpMGR.ULCLState == smf_context.IsOnlyULCL {

		psa1Path := bpMGR.PSA1Path
		psa2Path := bpMGR.PSA2Path
		var psa1NodeAfterUlcl *smf_context.DataPathNode
		var psa2NodeAfterUlcl *smf_context.DataPathNode
		var err error

		ulclIdx := bpMGR.ULCLIdx
		psa1NodeAfterUlcl = ulcl.DataPathToDN[psa1Path[ulclIdx+1].UPF.GetUPFID()].To
		psa2NodeAfterUlcl = ulcl.DataPathToDN[psa2Path[ulclIdx+1].UPF.GetUPFID()].To

		//Get the UPlinkPDR for PSA1
		var UpLinkForPSA1, UpLinkForPSA2 *smf_context.DataPathDownLink
		var DownLinkForPSA1, DownLinkForPSA2 *smf_context.DataPathUpLink
		//Todo:
		//Put every uplink to BPUplink
		upLinkIP := ulcl.DataPathToAN.UpLinkPDR.FAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address.String()
		if upLinkIP != psa1NodeAfterUlcl.UPF.UPIPInfo.Ipv4Address.String() {
			UpLinkForPSA1 = ulcl.BPUpLinkPDRs[psa1NodeAfterUlcl.UPF.GetUPFID()]
		} else {
			UpLinkForPSA1 = ulcl.DataPathToAN
			UpLinkForPSA1.DestinationIP = ulcl.DataPathToDN[psa1NodeAfterUlcl.UPF.GetUPFID()].DestinationIP
			UpLinkForPSA1.DestinationPort = ulcl.DataPathToDN[psa1NodeAfterUlcl.UPF.GetUPFID()].DestinationPort
		}

		UpLinkForPSA2 = smf_context.NewDataPathDownLink()
		UpLinkForPSA2.To = UpLinkForPSA1.To
		UpLinkForPSA2.DestinationIP = ulcl.DataPathToDN[psa2NodeAfterUlcl.UPF.GetUPFID()].DestinationIP
		UpLinkForPSA2.DestinationPort = ulcl.DataPathToDN[psa2NodeAfterUlcl.UPF.GetUPFID()].DestinationPort

		UpLinkForPSA2.UpLinkPDR, err = ulcl.UPF.AddPDR()
		if err != nil {
			logger.PduSessLog.Error(err)
		}

		UpLinkForPSA2.UpLinkPDR.Precedence = 32
		UpLinkForPSA2.UpLinkPDR.PDI = smf_context.PDI{
			SourceInterface: pfcpType.SourceInterface{
				//Todo:
				//Have to change source interface for different upf
				InterfaceValue: pfcpType.SourceInterfaceAccess,
			},
			LocalFTeid: &pfcpType.FTEID{
				V4:          true,
				Teid:        UpLinkForPSA1.UpLinkPDR.PDI.LocalFTeid.Teid,
				Ipv4Address: ulcl.UPF.UPIPInfo.Ipv4Address,
			},
			NetworkInstance: []byte(smContext.Dnn),
			UEIPAddress: &pfcpType.UEIPAddress{
				V4:          true,
				Ipv4Address: smContext.PDUAddress.To4(),
			},
		}
		UpLinkForPSA2.UpLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
		UpLinkForPSA2.UpLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
		UpLinkForPSA2.UpLinkPDR.State = smf_context.RULE_INITIAL

		UpLinkFARForPSA2 := UpLinkForPSA2.UpLinkPDR.FAR
		UpLinkFARForPSA2.ApplyAction.Forw = true
		UpLinkFARForPSA2.State = smf_context.RULE_INITIAL
		UpLinkFARForPSA2.ForwardingParameters = &smf_context.ForwardingParameters{
			DestinationInterface: pfcpType.DestinationInterface{
				InterfaceValue: pfcpType.DestinationInterfaceCore,
			},
			NetworkInstance: []byte(smContext.Dnn),
		}

		UpLinkFARForPSA2.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
		UpLinkFARForPSA2.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
		UpLinkFARForPSA2.ForwardingParameters.OuterHeaderCreation.Teid = psa2NodeAfterUlcl.GetUpLinkPDR().PDI.LocalFTeid.Teid
		UpLinkFARForPSA2.ForwardingParameters.OuterHeaderCreation.Ipv4Address = psa2NodeAfterUlcl.UPF.UPIPInfo.Ipv4Address

		UpLinkForPSA1.UpLinkPDR.State = smf_context.RULE_UPDATE
		UpLinkFARForPSA1 := UpLinkForPSA1.UpLinkPDR.FAR
		UpLinkFARForPSA1.State = smf_context.RULE_UPDATE
		UpLinkFARForPSA1.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
		UpLinkFARForPSA1.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
		UpLinkFARForPSA1.ForwardingParameters.OuterHeaderCreation.Teid = psa1NodeAfterUlcl.GetUpLinkPDR().PDI.LocalFTeid.Teid
		UpLinkFARForPSA1.ForwardingParameters.OuterHeaderCreation.Ipv4Address = psa1NodeAfterUlcl.UPF.UPIPInfo.Ipv4Address

		ulcl.BPUpLinkPDRs[psa2NodeAfterUlcl.UPF.GetUPFID()] = UpLinkForPSA2
		upLinks := []*smf_context.DataPathDownLink{UpLinkForPSA1, UpLinkForPSA2}

		for _, link := range upLinks {
			FlowDespcription := flowdesc.NewIPFilterRule()
			err = FlowDespcription.SetAction(true) //permit
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDirection(true) //uplink
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDestinationIp(link.DestinationIP)
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDestinationPorts(link.DestinationPort)
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetSourceIp(smContext.PDUAddress.To4().String())
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}

			FlowDespcriptionStr, err := FlowDespcription.Encode()

			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when encoding flow despcription: %s\n", err)
			}

			link.UpLinkPDR.PDI.SDFFilter = &pfcpType.SDFFilter{
				Bid:                     false,
				Fl:                      false,
				Spi:                     false,
				Ttc:                     false,
				Fd:                      true,
				LengthOfFlowDescription: uint16(len(FlowDespcriptionStr)),
				FlowDescription:         []byte(FlowDespcriptionStr),
			}

		}

		DownLinkForPSA1 = ulcl.DataPathToDN[psa1NodeAfterUlcl.UPF.GetUPFID()]
		DownLinkForPSA2 = ulcl.DataPathToDN[psa2NodeAfterUlcl.UPF.GetUPFID()]

		DownLinkForPSA2.DownLinkPDR, err = ulcl.UPF.AddPDR()
		if err != nil {
			logger.PduSessLog.Error(err)
		}

		teid, err := ulcl.UPF.GenerateTEID()
		DownLinkForPSA2.DownLinkPDR.Precedence = 32
		DownLinkForPSA2.DownLinkPDR.PDI = smf_context.PDI{
			SourceInterface: pfcpType.SourceInterface{
				//Todo:
				//Have to change source interface for different upf
				InterfaceValue: pfcpType.SourceInterfaceAccess,
			},
			LocalFTeid: &pfcpType.FTEID{
				V4:          true,
				Teid:        teid,
				Ipv4Address: ulcl.UPF.UPIPInfo.Ipv4Address,
			},
			NetworkInstance: []byte(smContext.Dnn),
			UEIPAddress: &pfcpType.UEIPAddress{
				V4:          true,
				Ipv4Address: smContext.PDUAddress.To4(),
			},
		}
		DownLinkForPSA2.DownLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
		DownLinkForPSA2.DownLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
		DownLinkForPSA2.DownLinkPDR.State = smf_context.RULE_INITIAL

		DownLinkFarForPSA2 := DownLinkForPSA2.DownLinkPDR.FAR
		DownLinkFarForPSA2.ApplyAction.Forw = true
		DownLinkFarForPSA2.State = smf_context.RULE_INITIAL
		DownLinkFarForPSA2.ForwardingParameters = &smf_context.ForwardingParameters{
			DestinationInterface: pfcpType.DestinationInterface{
				InterfaceValue: pfcpType.DestinationInterfaceCore,
			},
			NetworkInstance: []byte(smContext.Dnn),
		}

		//Todo:
		//Delete this after finishing new downlinking userplane
		fmt.Println(DownLinkForPSA1)
		//Todo:
		//Uncommemt after finishing new downlinking userplane
		//DownLinkFarForPSA2.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
		//DownLinkFarForPSA2.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
		// DownLinkFarFoDPSA2.ForwardingParameters.OuterHeaderCreation.Teid = DownLinkForPSA1.PDR.PDI.LocalFTeid.Teid
		// DownLinkFarForPSA2.ForwardingParameters.OuterHeaderCreation.Ipv4Address = DownLinkForPSA1.PDR.FAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address

		// addr := net.UDPAddr{
		// 	IP:   ulcl.Next[psa1NodeAfterUlcl.UPF.GetUPFID()].To.UPF.NodeID.NodeIdValue,
		// 	Port: pfcpUdp.PFCP_PORT,
		// }
		addr := net.UDPAddr{
			IP:   ulcl.UPF.NodeID.NodeIdValue,
			Port: pfcpUdp.PFCP_PORT,
		}
		pdr_list := []*smf_context.PDR{UpLinkForPSA1.UpLinkPDR, UpLinkForPSA2.UpLinkPDR, DownLinkForPSA2.DownLinkPDR}
		far_list := []*smf_context.FAR{UpLinkForPSA1.UpLinkPDR.FAR, UpLinkForPSA2.UpLinkPDR.FAR, DownLinkForPSA2.DownLinkPDR.FAR}
		bar_list := []*smf_context.BAR{}

		pfcp_message.SendPfcpSessionModificationRequest(&addr, smContext, pdr_list, far_list, bar_list)
		logger.PfcpLog.Info("[SMF] Establish ULCL msg has been send")
	}
}

func UpdatePSA2DownLink(smContext *smf_context.SMContext) {
	logger.PduSessLog.Traceln("In UpdatePSA2DownLink")

	bpMGR := smContext.BPManager
	ulcl := bpMGR.ULCLDataPathNode

	if bpMGR.ULCLState == smf_context.IsOnlyULCL {
		psa2Path := bpMGR.PSA2Path

		var psa2NodeAfterUlcl *smf_context.DataPathNode

		ulclIdx := bpMGR.ULCLIdx
		psa2NodeAfterUlcl = ulcl.DataPathToDN[psa2Path[ulclIdx+1].UPF.GetUPFID()].To
		far_list := []*smf_context.FAR{}

		if psa2NodeAfterUlcl.IsAnchorUPF() {

			updateDownLinkFAR := psa2NodeAfterUlcl.DLDataPathLinkForPSA.DownLinkPDR.FAR
			updateDownLinkFAR.State = smf_context.RULE_UPDATE
			updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
			updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
			updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = ulcl.DataPathToDN[psa2NodeAfterUlcl.UPF.GetUPFID()].DownLinkPDR.PDI.LocalFTeid.Teid
			updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = ulcl.UPF.UPIPInfo.Ipv4Address

			far_list = append(far_list, updateDownLinkFAR)
		} else {

			for _, updateDownLink := range psa2NodeAfterUlcl.DataPathToDN {

				if updateDownLink.DownLinkPDR != nil {
					updateDownLinkFAR := updateDownLink.DownLinkPDR.FAR
					updateDownLinkFAR.State = smf_context.RULE_UPDATE
					updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
					updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
					updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = ulcl.DataPathToDN[psa2NodeAfterUlcl.UPF.GetUPFID()].DownLinkPDR.PDI.LocalFTeid.Teid
					updateDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = ulcl.UPF.UPIPInfo.Ipv4Address

					far_list = append(far_list, updateDownLinkFAR)
				}
			}
		}

		addr := net.UDPAddr{
			IP:   psa2NodeAfterUlcl.UPF.NodeID.NodeIdValue,
			Port: pfcpUdp.PFCP_PORT,
		}
		pdr_list := []*smf_context.PDR{}
		bar_list := []*smf_context.BAR{}

		pfcp_message.SendPfcpSessionModificationRequest(&addr, smContext, pdr_list, far_list, bar_list)
		logger.PfcpLog.Info("[SMF] Update PSA2 downlink msg has been send")
	}
}

// func UpdatePSA1DownLink(smContext *smf_context.SMContext) {

// }

// func AddRoutingRule(smContext *smf_context.SMContext, upfNode *smf_context.UEPathNode) {
// 	upfName := upfNode.UPFName
// 	upfNodeID := smf_context.GetUserPlaneInformation().GetUPFNodeIDByName(upfName)

// 	var newULPDR *smf_context.PDR
// 	if upfNode.IsLeafNode() {
// 		newULPDR = smContext.Tunnel.Node.AddPDR()
// 		newULPDR.InitializePDR(smContext)

// 	} else {
// 		newULPDR = smContext.Tunnel.Node.AddPDR()
// 		newULPDR.InitializePDR(smContext)

// 		// has only one child
// 		var childIP []byte
// 		for _, child_node := range upfNode.GetChild() {
// 			childIP = smf_context.GetUserPlaneInformation().GetUPFIPByName(child_node.UPFName)
// 		}

// 		fp := newULPDR.FAR.ForwardingParameters
// 		fp.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
// 		fp.OuterHeaderCreation.Teid = 10 //?
// 		fp.OuterHeaderCreation.Ipv4Address = childIP
// 	}

// 	pdr_list := []*smf_context.PDR{newULPDR}
// 	far_list := []*smf_context.FAR{newULPDR.FAR}
// 	bar_list := []*smf_context.BAR{}

// 	addr := net.UDPAddr{
// 		IP:   upfNodeID.NodeIdValue,
// 		Port: pfcpUdp.PFCP_PORT,
// 	}

// 	pfcp_message.SendPfcpSessionEstablishmentRequestForULCL(&addr, smContext, pdr_list, far_list, bar_list)
// 	fmt.Println("[SMF] Add Routing Rule msg has been send")
// }

// func AddBranchingRule(smContext *smf_context.SMContext, upfNode *smf_context.UEPathNode) {
// 	upfName := upfNode.UPFName
// 	upfNodeID := smf_context.GetUserPlaneInformation().GetUPFNodeIDByName(upfName)
// 	upfIP := upfNodeID.ResolveNodeIdToIp().String()

// 	//tunnel := smContext.Tunnel

// 	pdr_list := make([]*smf_context.PDR, 0)
// 	far_list := make([]*smf_context.FAR, 0)
// 	bar_list := make([]*smf_context.BAR, 0)

// 	//upfULPDR := tunnel.ULPDR

// 	for _, child_node := range upfNode.GetChild() {
// 		var err error
// 		child_name := child_node.UPFName
// 		childEndPoint := upfNode.EndPointOfEachChild[child_name]
// 		FlowDespcription := flowdesc.NewIPFilterRule()

// 		err = FlowDespcription.SetAction(true) //permit
// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
// 		}
// 		err = FlowDespcription.SetDirection(true) //uplink
// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
// 		}
// 		err = FlowDespcription.SetDestinationIp(childEndPoint.EndPointIP)
// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
// 		}
// 		err = FlowDespcription.SetDestinationPorts(childEndPoint.EndPointPort)
// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
// 		}
// 		err = FlowDespcription.SetSourceIp(upfIP)
// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
// 		}

// 		fmt.Println("[SMF] PFCP port: ", strconv.Itoa(pfcpUdp.PFCP_PORT))

// 		err = FlowDespcription.SetSourcePorts(strconv.Itoa(pfcpUdp.PFCP_PORT))
// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
// 		}
// 		err = FlowDespcription.SetProtocal(0xfc)
// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
// 		}

// 		FlowDespcriptionStr, err := FlowDespcription.Encode()

// 		if err != nil {
// 			logger.PduSessLog.Errorf("Error occurs when encoding flow despcription: %s\n", err)
// 		}

// 		newULPDR := smContext.Tunnel.Node.AddPDR()
// 		newULPDR.InitializePDR(smContext)
// 		newULPDR.Precedence = 30
// 		newULPDR.PDI.SDFFilter = &pfcpType.SDFFilter{
// 			Bid:                     false,
// 			Fl:                      false,
// 			Spi:                     false,
// 			Ttc:                     false,
// 			Fd:                      true,
// 			LengthOfFlowDescription: uint16(len(FlowDespcriptionStr)),
// 			FlowDescription:         []byte(FlowDespcriptionStr),
// 		}

// 		fp := newULPDR.FAR.ForwardingParameters
// 		fp.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
// 		fp.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
// 		fp.OuterHeaderCreation.Teid = 10 //?
// 		fp.OuterHeaderCreation.Ipv4Address = smf_context.GetUserPlaneInformation().GetUPFIPByName(child_name)

// 		pdr_list = append(pdr_list, newULPDR)
// 		far_list = append(far_list, newULPDR.FAR)
// 		//has change to: Modify existing pdr first, and then create new pdr.
// 		// if len(upfULPDR) > idx {
// 		// 	// modify existing pdr

// 		// } else {
// 		// 	// create new pdr

// 	}

// 	//PDR2

// 	addr := net.UDPAddr{
// 		IP:   upfNodeID.NodeIdValue,
// 		Port: pfcpUdp.PFCP_PORT,
// 	}

// 	pfcp_message.SendPfcpSessionModificationRequest(&addr, smContext, pdr_list, far_list, bar_list)
// 	fmt.Println("[SMF] Add Branching Rule msg has been send")
// }
