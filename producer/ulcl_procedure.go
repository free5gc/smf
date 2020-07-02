package producer

import (
	"free5gc/lib/flowdesc"
	"free5gc/lib/pfcp/pfcpType"
	"free5gc/lib/pfcp/pfcpUdp"
	"free5gc/src/smf/context"
	"free5gc/src/smf/logger"
	"free5gc/src/smf/pfcp/message"
	"net"
	"reflect"
	"time"
)

func AddPDUSessionAnchorAndULCL(smContext *context.SMContext) {

	bpManager := smContext.BPManager
	//select PSA2
	bpManager.SelectPSA2(smContext)
	smContext.AllocateLocalSEIDForDataPath(bpManager.ActivatingPath)
	//select an upf as ULCL
	err := bpManager.FindULCL(smContext)
	if err != nil {
		logger.PduSessLog.Errorln(err)
		return
	}

	//Allocate Path PDR and TEID
	bpManager.ActivatingPath.ActivateTunnelAndPDR(smContext)
	//N1N2MessageTransfer Here

	//Establish PSA2
	EstablishPSA2(smContext)
	//workaround for waitng PFCP response
	time.Sleep(time.Second * 1)

	EstablishRANTunnelInfo(smContext)
	//Establish ULCL
	EstablishULCL(smContext)
	//workaround for waitng PFCP response
	time.Sleep(time.Second * 1)

	//updatePSA1 downlink
	//UpdatePSA1DownLink(smContext)
	//updatePSA2 downlink
	UpdatePSA2DownLink(smContext)
	//workaround for waitng PFCP response
	time.Sleep(time.Second * 1)
	//update AN for new CN Info
	UpdateRANAndIUPFUpLink(smContext)

}

func EstablishPSA2(smContext *context.SMContext) {
	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	ulcl := bpMGR.ULCL
	logger.PduSessLog.Infoln("In EstablishPSA2")
	nodeAfterULCL := false
	for curDataPathNode := activatingPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {

		if nodeAfterULCL {
			addr := net.UDPAddr{
				IP:   curDataPathNode.UPF.NodeID.NodeIdValue,
				Port: pfcpUdp.PFCP_PORT,
			}

			logger.PduSessLog.Traceln("Send to upf addr: ", addr.String())

			upLinkPDR := curDataPathNode.UpLinkTunnel.PDR

			pdrList := []*context.PDR{upLinkPDR}
			farList := []*context.FAR{upLinkPDR.FAR}
			barList := []*context.BAR{}

			lastNode := curDataPathNode.Prev()

			if lastNode != nil && !reflect.DeepEqual(lastNode.UPF.NodeID, ulcl.NodeID) {
				downLinkPDR := curDataPathNode.DownLinkTunnel.PDR
				pdrList = append(pdrList, downLinkPDR)
				farList = append(farList, downLinkPDR.FAR)
			}

			message.SendPfcpSessionEstablishmentRequest(curDataPathNode.UPF.NodeID, smContext, pdrList, farList, barList)
		} else {
			if reflect.DeepEqual(curDataPathNode.UPF.NodeID, ulcl.NodeID) {
				nodeAfterULCL = true
			}
		}

	}

	logger.PduSessLog.Traceln("End of EstablishPSA2")
}

func EstablishULCL(smContext *context.SMContext) {

	logger.PduSessLog.Traceln("In EstablishULCL")

	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	dest := activatingPath.Destination
	ulcl := bpMGR.ULCL

	//find updatedUPF in activatingPath
	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			UPLinkPDR := curDPNode.UpLinkTunnel.PDR
			DownLinkPDR := curDPNode.DownLinkTunnel.PDR
			UPLinkPDR.State = context.RULE_INITIAL

			FlowDespcription := flowdesc.NewIPFilterRule()
			err := FlowDespcription.SetAction(true) //permit
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDirection(true) //uplink
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDestinationIp(dest.DestinationIP)
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDestinationPorts(dest.DestinationPort)
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

			UPLinkPDR.PDI.SDFFilter = &pfcpType.SDFFilter{
				Bid:                     false,
				Fl:                      false,
				Spi:                     false,
				Ttc:                     false,
				Fd:                      true,
				LengthOfFlowDescription: uint16(len(FlowDespcriptionStr)),
				FlowDescription:         []byte(FlowDespcriptionStr),
			}

			UPLinkPDR.Precedence = 30

			pdrList := []*context.PDR{UPLinkPDR, DownLinkPDR}
			farList := []*context.FAR{UPLinkPDR.FAR, DownLinkPDR.FAR}
			barList := []*context.BAR{}

			message.SendPfcpSessionModificationRequest(ulcl.NodeID, smContext, pdrList, farList, barList)
			break
		}
	}

	logger.PfcpLog.Info("[SMF] Establish ULCL msg has been send")

}

func UpdatePSA2DownLink(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdatePSA2DownLink")

	bpMGR := smContext.BPManager
	ulcl := bpMGR.ULCL
	activatingPath := bpMGR.ActivatingPath

	farList := []*context.FAR{}
	pdrList := []*context.PDR{}
	barList := []*context.BAR{}

	for curDataPathNode := activatingPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		lastNode := curDataPathNode.Prev()

		if lastNode != nil {
			if reflect.DeepEqual(lastNode.UPF.NodeID, ulcl.NodeID) {
				downLinkPDR := curDataPathNode.DownLinkTunnel.PDR
				downLinkPDR.State = context.RULE_INITIAL
				downLinkPDR.FAR.State = context.RULE_INITIAL

				pdrList = append(pdrList, downLinkPDR)
				farList = append(farList, downLinkPDR.FAR)
				message.SendPfcpSessionModificationRequest(curDataPathNode.UPF.NodeID, smContext, pdrList, farList, barList)
				logger.PfcpLog.Info("[SMF] Update PSA2 downlink msg has been send")
				break
			}
		}
	}

}

func EstablishRANTunnelInfo(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdatePSA2DownLink")

	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath

	defaultPath := smContext.Tunnel.DataPathPool.GetDefaultPath()
	defaultANUPF := defaultPath.FirstDPNode

	activatingANUPF := activatingPath.FirstDPNode

	//Uplink ANUPF In TEID
	activatingANUPF.UpLinkTunnel.TEID = defaultANUPF.UpLinkTunnel.TEID
	activatingANUPF.UpLinkTunnel.PDR.PDI.LocalFTeid.Teid = defaultANUPF.UpLinkTunnel.PDR.PDI.LocalFTeid.Teid

	//Downlink ANUPF OutTEID

	defaultANUPFDLFAR := defaultANUPF.DownLinkTunnel.PDR.FAR
	activatingANUPFDLFAR := activatingANUPF.DownLinkTunnel.PDR.FAR
	activatingANUPFDLFAR.ApplyAction = pfcpType.ApplyAction{
		Buff: false,
		Drop: false,
		Dupl: false,
		Forw: true,
		Nocp: false,
	}
	activatingANUPFDLFAR.ForwardingParameters = &context.ForwardingParameters{
		DestinationInterface: pfcpType.DestinationInterface{
			InterfaceValue: pfcpType.DestinationInterfaceAccess,
		},
		NetworkInstance: []byte(smContext.Dnn),
	}

	activatingANUPFDLFAR.State = context.RULE_INITIAL
	activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
	activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
	activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Teid = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Teid
	activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address

}

func UpdateRANAndIUPFUpLink(smContext *context.SMContext) {

	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	dest := activatingPath.Destination
	ulcl := bpMGR.ULCL

	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {

		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			break
		} else {
			UPLinkPDR := curDPNode.UpLinkTunnel.PDR
			DownLinkPDR := curDPNode.DownLinkTunnel.PDR
			UPLinkPDR.State = context.RULE_INITIAL
			DownLinkPDR.State = context.RULE_INITIAL

			if _, exist := bpMGR.UpdatedBranchingPoint[curDPNode.UPF]; exist {
				//add SDF Filter
				FlowDespcription := flowdesc.NewIPFilterRule()
				err := FlowDespcription.SetAction(true) //permit
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}
				err = FlowDespcription.SetDirection(true) //uplink
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}
				err = FlowDespcription.SetDestinationIp(dest.DestinationIP)
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}
				err = FlowDespcription.SetDestinationPorts(dest.DestinationPort)
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

				UPLinkPDR.PDI.SDFFilter = &pfcpType.SDFFilter{
					Bid:                     false,
					Fl:                      false,
					Spi:                     false,
					Ttc:                     false,
					Fd:                      true,
					LengthOfFlowDescription: uint16(len(FlowDespcriptionStr)),
					FlowDescription:         []byte(FlowDespcriptionStr),
				}
			}

			pdrList := []*context.PDR{UPLinkPDR, DownLinkPDR}
			farList := []*context.FAR{UPLinkPDR.FAR, DownLinkPDR.FAR}
			barList := []*context.BAR{}

			message.SendPfcpSessionModificationRequest(curDPNode.UPF.NodeID, smContext, pdrList, farList, barList)
		}
	}

}
