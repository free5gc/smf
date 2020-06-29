package producer

// import (
// 	"free5gc/lib/pfcp/pfcpType"
// 	"free5gc/lib/pfcp/pfcpUdp"
// 	"free5gc/src/smf/context"
// 	"free5gc/src/smf/logger"
// 	"free5gc/src/smf/pfcp/message"
// 	"net"
// )

// func SetUPPSA2Path(smContext *context.SMContext, psa2_path_after_ulcl []*context.UPNode, start_node *context.DataPathNode) {

// 	lowerBound := 0
// 	upperBound := len(psa2_path_after_ulcl) - 1
// 	curDataPathNode := start_node
// 	var downLink *context.DataPathUpLink

// 	//Allocate upLink and downLink PDR
// 	logger.PduSessLog.Traceln("In SetUPPSA2Path")
// 	for i, node := range psa2_path_after_ulcl {

// 		logger.PduSessLog.Traceln("Node ", i, ": ", node.UPF.GetUPFIP())
// 	}
// 	for idx := range psa2_path_after_ulcl {

// 		upLink := curDataPathNode.GetUpLink()

// 		teid, err := curDataPathNode.UPF.GenerateTEID()

// 		if err != nil {
// 			logger.PduSessLog.Error(err)
// 		}

// 		upLink.UpLinkPDR, err = curDataPathNode.UPF.AddPDR()
// 		smContext.PutPDRtoPFCPSession(curDataPathNode.UPF.NodeID, upLink.UpLinkPDR)
// 		if err != nil {
// 			logger.PduSessLog.Error(err)
// 		}

// 		upLink.UpLinkPDR.Precedence = 32
// 		upLink.UpLinkPDR.PDI = context.PDI{
// 			SourceInterface: pfcpType.SourceInterface{
// 				//Todo:
// 				//Have to change source interface for different upf
// 				InterfaceValue: pfcpType.SourceInterfaceAccess,
// 			},
// 			LocalFTeid: &pfcpType.FTEID{
// 				V4:          true,
// 				Teid:        teid,
// 				Ipv4Address: curDataPathNode.UPF.UPIPInfo.Ipv4Address,
// 			},
// 			NetworkInstance: []byte(smContext.Dnn),
// 			UEIPAddress: &pfcpType.UEIPAddress{
// 				V4:          true,
// 				Ipv4Address: smContext.PDUAddress.To4(),
// 			},
// 		}
// 		upLink.UpLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
// 		upLink.UpLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
// 		upLink.UpLinkPDR.State = context.RULE_INITIAL

// 		upLink.UpLinkPDR.FAR.ApplyAction.Forw = true
// 		upLink.UpLinkPDR.FAR.State = context.RULE_INITIAL
// 		upLink.UpLinkPDR.FAR.ForwardingParameters = &context.ForwardingParameters{
// 			DestinationInterface: pfcpType.DestinationInterface{
// 				InterfaceValue: pfcpType.DestinationInterfaceCore,
// 			},
// 			NetworkInstance: []byte(smContext.Dnn),
// 		}

// 		if curDataPathNode.IsAnchorUPF() {

// 			downLink = curDataPathNode.DLDataPathLinkForPSA
// 		} else {
// 			nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
// 			downLink = curDataPathNode.DataPathToDN[nextUPFID]
// 		}

// 		downLink.DownLinkPDR, err = curDataPathNode.UPF.AddPDR()
// 		smContext.PutPDRtoPFCPSession(curDataPathNode.UPF.NodeID, downLink.DownLinkPDR)
// 		if err != nil {
// 			logger.PduSessLog.Error(err)
// 		}

// 		teid, err = curDataPathNode.UPF.GenerateTEID()

// 		if err != nil {
// 			logger.PduSessLog.Error(err)
// 		}

// 		downLink.DownLinkPDR.Precedence = 32
// 		downLink.DownLinkPDR.PDI = context.PDI{
// 			SourceInterface: pfcpType.SourceInterface{
// 				//Todo:
// 				//Have to change source interface for different upf
// 				InterfaceValue: pfcpType.SourceInterfaceAccess,
// 			},
// 			LocalFTeid: &pfcpType.FTEID{
// 				V4:          true,
// 				Teid:        teid,
// 				Ipv4Address: curDataPathNode.UPF.UPIPInfo.Ipv4Address,
// 			},
// 			NetworkInstance: []byte(smContext.Dnn),
// 			UEIPAddress: &pfcpType.UEIPAddress{
// 				V4:          true,
// 				Ipv4Address: smContext.PDUAddress.To4(),
// 			},
// 		}

// 		if !curDataPathNode.IsAnchorUPF() {
// 			downLink.DownLinkPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
// 			downLink.DownLinkPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
// 			downLink.DownLinkPDR.State = context.RULE_INITIAL
// 		}

// 		downLink.DownLinkPDR.FAR.ApplyAction.Forw = true
// 		downLink.DownLinkPDR.FAR.State = context.RULE_INITIAL
// 		downLink.DownLinkPDR.FAR.ForwardingParameters = &context.ForwardingParameters{
// 			DestinationInterface: pfcpType.DestinationInterface{
// 				InterfaceValue: pfcpType.DestinationInterfaceCore,
// 			},
// 			NetworkInstance: []byte(smContext.Dnn),
// 		}

// 		if idx != upperBound {
// 			curDataPathNode = downLink.To
// 		}

// 	}

// 	curDataPathNode = start_node

// 	//Allocate upLink and downLink TEID
// 	for idx := range psa2_path_after_ulcl {

// 		switch idx {
// 		case lowerBound:

// 			if !curDataPathNode.IsAnchorUPF() {
// 				nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
// 				downLink = curDataPathNode.DataPathToDN[nextUPFID]
// 				allocatedDownLinkTEID := downLink.DownLinkPDR.PDI.LocalFTeid.Teid
// 				child := downLink.To

// 				var childDownLinkFAR *context.FAR

// 				if child.IsAnchorUPF() {

// 					childDownLinkFAR = child.DLDataPathLinkForPSA.DownLinkPDR.FAR
// 				} else {

// 					nextNextUPFID := psa2_path_after_ulcl[idx+2].UPF.GetUPFID()
// 					childDownLinkFAR = child.DataPathToDN[nextNextUPFID].DownLinkPDR.FAR
// 				}
// 				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
// 				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
// 				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedDownLinkTEID)
// 				childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address

// 			}

// 		case upperBound:

// 			parent := curDataPathNode.GetParent()
// 			if parent != nil {
// 				allocatedUPLinkTEID := curDataPathNode.DataPathToAN.UpLinkPDR.PDI.LocalFTeid.Teid
// 				parentUpLinkFAR := parent.GetUpLinkFAR()

// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedUPLinkTEID)
// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address
// 			}
// 		default:

// 			nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
// 			downLink = curDataPathNode.DataPathToDN[nextUPFID]
// 			allocatedDownLinkTEID := downLink.DownLinkPDR.PDI.LocalFTeid.Teid
// 			child := downLink.To

// 			var childDownLinkFAR *context.FAR

// 			if child.IsAnchorUPF() {

// 				childDownLinkFAR = child.DLDataPathLinkForPSA.DownLinkPDR.FAR
// 			} else {

// 				nextNextUPFID := psa2_path_after_ulcl[idx+2].UPF.GetUPFID()
// 				childDownLinkFAR = child.DataPathToDN[nextNextUPFID].DownLinkPDR.FAR
// 			}
// 			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
// 			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
// 			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedDownLinkTEID)
// 			childDownLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address

// 			parent := curDataPathNode.GetParent()
// 			if parent != nil {
// 				allocatedUPLinkTEID := curDataPathNode.DataPathToAN.UpLinkPDR.PDI.LocalFTeid.Teid
// 				parentUpLinkFAR := parent.GetUpLinkFAR()

// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Teid = uint32(allocatedUPLinkTEID)
// 				parentUpLinkFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = curDataPathNode.UPF.UPIPInfo.Ipv4Address
// 			}

// 		}

// 		if idx != upperBound {
// 			curDataPathNode = downLink.To
// 		}
// 	}

// 	curDataPathNode = start_node
// 	logger.PduSessLog.Traceln("Start Node is PSA: ", curDataPathNode.IsAnchorUPF())
// 	for idx := range psa2_path_after_ulcl {

// 		addr := net.UDPAddr{
// 			IP:   curDataPathNode.UPF.NodeID.NodeIdValue,
// 			Port: pfcpUdp.PFCP_PORT,
// 		}

// 		logger.PduSessLog.Traceln("Send to upf addr: ", addr.String())

// 		upLink := curDataPathNode.DataPathToAN

// 		if curDataPathNode.IsAnchorUPF() {

// 			downLink = curDataPathNode.DLDataPathLinkForPSA
// 		} else {

// 			nextUPFID := psa2_path_after_ulcl[idx+1].UPF.GetUPFID()
// 			downLink = curDataPathNode.DataPathToDN[nextUPFID]
// 		}

// 		pdrList := []*context.PDR{upLink.UpLinkPDR, downLink.DownLinkPDR}
// 		farList := []*context.FAR{upLink.UpLinkPDR.FAR, downLink.DownLinkPDR.FAR}
// 		barList := []*context.BAR{}

// 		message.SendPfcpSessionEstablishmentRequestForULCL(curDataPathNode.UPF.NodeID, smContext, pdrList, farList, barList)
// 	}

// }
