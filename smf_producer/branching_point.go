package smf_producer

import (
	//"gofree5gc/lib/flowdesc"
	"gofree5gc/lib/pfcp/pfcpType"
	// "gofree5gc/lib/pfcp/pfcpUdp"
	// "gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/smf_context"
	// "gofree5gc/src/smf/smf_pfcp/pfcp_message"
	// "net"
)

// var sdf_filter_id uint32

// func getSDFFilterID() uint32 {
// 	sdf_filter_id++
// 	return sdf_filter_id
// }

func CheckBranchingPoint(nodeID *pfcpType.NodeID, smContext *smf_context.SMContext) bool {

	upfIP := nodeID.ResolveNodeIdToIp().String()
	upfName := smf_context.SMF_Self().UserPlaneInformation.UPFIPToName[upfIP]

	ueRoutingGraph := smf_context.SMF_Self().UERoutingGraphs[smContext.Supi]

	return ueRoutingGraph.IsBranchingPoint(upfName)

	// tunnel := smContext.Tunnel

	// //PDR1

	// FlowDespcription := flowdesc.NewIPFilterRule()

	// FlowDespcription.SetAction(true)     //permit
	// FlowDespcription.SetDirection(false) //uplink
	// FlowDespcription.SetDestinationIp("0.0.0.0")
	// FlowDespcription.SetDestinationPorts("0000")
	// FlowDespcription.SetSourceIp("0.0.0.0")
	// FlowDespcription.SetSourcePorts("0000")
	// FlowDespcription.SetProtocal(0xfc)

	// FlowDespcriptionStr, err := FlowDespcription.Encode()

	// if err == nil {
	// 	logger.PduSessLog.Errorf("Error occurs when encoding flow despcription: %s\n", err)
	// }

	// tunnel.ULPDR.State = smf_context.RULE_UPDATE
	// tunnel.ULPDR.PDI.SDFFilter = &pfcpType.SDFFilter{
	// 	Bid:                     false,
	// 	Fl:                      false,
	// 	Spi:                     false,
	// 	Ttc:                     false,
	// 	Fd:                      true,
	// 	LengthOfFlowDescription: uint16(len(FlowDespcriptionStr)),
	// 	FlowDescription:         []byte(FlowDespcriptionStr),
	// 	SdfFilterId:             getSDFFilterID(),
	// }
	// //PDR2

	// FlowDespcription = flowdesc.NewIPFilterRule()

	// FlowDespcription.SetAction(true)     //permit
	// FlowDespcription.SetDirection(false) //uplink
	// FlowDespcription.SetDestinationIp("0.0.0.0")
	// FlowDespcription.SetDestinationPorts("0000")
	// FlowDespcription.SetSourceIp("0.0.0.0")
	// FlowDespcription.SetSourcePorts("0000")
	// FlowDespcription.SetProtocal(0xfc)

	// FlowDespcriptionStr, err = FlowDespcription.Encode()

	// if err == nil {
	// 	logger.PduSessLog.Errorf("Error occurs when encoding flow despcription: %s\n", err)
	// }

	// newULPDR := smContext.Tunnel.Node.AddPDR()
	// newULPDR.State = smf_context.RULE_INITIAL
	// newULPDR.Precedence = 32
	// newULPDR.PDI = smf_context.PDI{
	// 	SourceInterface: pfcpType.SourceInterface{
	// 		InterfaceValue: pfcpType.SourceInterfaceAccess,
	// 	},
	// 	LocalFTeid: pfcpType.FTEID{
	// 		V4:          true,
	// 		Teid:        tunnel.ULTEID,
	// 		Ipv4Address: tunnel.Node.UPIPInfo.Ipv4Address,
	// 	},
	// 	NetworkInstance: []byte(smContext.Dnn),
	// 	UEIPAddress: &pfcpType.UEIPAddress{
	// 		V4:          true,
	// 		Ipv4Address: smContext.PDUAddress.To4(),
	// 	},
	// }
	// newULPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
	// newULPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4
	// newULPDR.FAR.ApplyAction.Forw = true
	// newULPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
	// 	DestinationInterface: pfcpType.DestinationInterface{
	// 		InterfaceValue: pfcpType.DestinationInterfaceCore,
	// 	},
	// 	NetworkInstance: []byte(smContext.Dnn),
	// }

	// newULPDR.PDI.SDFFilter = &pfcpType.SDFFilter{
	// 	Bid:                     false,
	// 	Fl:                      false,
	// 	Spi:                     false,
	// 	Ttc:                     false,
	// 	Fd:                      true,
	// 	LengthOfFlowDescription: uint16(len(FlowDespcriptionStr)),
	// 	FlowDescription:         []byte(FlowDespcriptionStr),
	// 	SdfFilterId:             getSDFFilterID(),
	// }

	// addr := net.UDPAddr{
	// 	IP:   smContext.Tunnel.Node.NodeID.NodeIdValue,
	// 	Port: pfcpUdp.PFCP_PORT,
	// }

	// pdr_list := []*smf_context.PDR{tunnel.ULPDR}
	// far_list := []*smf_context.FAR{}
	// bar_list := []*smf_context.BAR{}

	// pfcp_message.SendPfcpSessionModificationRequest(&addr, smContext, pdr_list, far_list, bar_list)

}
