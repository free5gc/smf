package producer

import (
	"net"
	"reflect"

	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/pfcp/pfcpUdp"
	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/util/flowdesc"
)

func AddPDUSessionAnchorAndULCL(smContext *context.SMContext) {
	bpMGR := smContext.BPManager

	switch bpMGR.AddingPSAState {
	case context.ActivatingDataPath:
		for {
			// select PSA2
			bpMGR.SelectPSA2(smContext)
			if len(bpMGR.ActivatedPaths) == len(smContext.Tunnel.DataPathPool) {
				break
			}
			smContext.AllocateLocalSEIDForDataPath(bpMGR.ActivatingPath)
			// select an upf as ULCL
			err := bpMGR.FindULCL(smContext)
			if err != nil {
				logger.PduSessLog.Errorln(err)
				return
			}

			// Allocate Path PDR and TEID
			bpMGR.ActivatingPath.ActivateTunnelAndPDR(smContext, 255)
			// N1N2MessageTransfer Here

			// Establish PSA2
			EstablishPSA2(smContext)

			EstablishRANTunnelInfo(smContext)
			// Establish ULCL
			EstablishULCL(smContext)

			UpdatePSA2DownLink(smContext)

			UpdateRANAndIUPFUpLink(smContext)
		}
	default:
		logger.CtxLog.Warnln("unexpected status")
	}
}

func EstablishPSA2(smContext *context.SMContext) {
	logger.PduSessLog.Infoln("Establish PSA2")
	bpMGR := smContext.BPManager
	bpMGR.PendingUPF = make(context.PendingUPF)
	activatingPath := bpMGR.ActivatingPath
	ulcl := bpMGR.ULCL
	nodeAfterULCL := false
	resChan := make(chan SendPfcpResult)

	for curDataPathNode := activatingPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		if nodeAfterULCL {
			addr := net.UDPAddr{
				IP:   curDataPathNode.UPF.NodeID.ResolveNodeIdToIp(),
				Port: pfcpUdp.PFCP_PORT,
			}

			logger.PduSessLog.Traceln("Send to upf addr: ", addr.String())

			upLinkPDR := curDataPathNode.UpLinkTunnel.PDR

			pdrList := []*context.PDR{upLinkPDR}
			farList := []*context.FAR{upLinkPDR.FAR}
			barList := []*context.BAR{}
			qerList := upLinkPDR.QER

			lastNode := curDataPathNode.Prev()

			if lastNode != nil && !reflect.DeepEqual(lastNode.UPF.NodeID, ulcl.NodeID) {
				downLinkPDR := curDataPathNode.DownLinkTunnel.PDR
				pdrList = append(pdrList, downLinkPDR)
				farList = append(farList, downLinkPDR.FAR)
			}
			pfcpState := &PFCPState{
				upf:     curDataPathNode.UPF,
				pdrList: pdrList,
				farList: farList,
				barList: barList,
				qerList: qerList,
			}

			curDPNodeIP := curDataPathNode.UPF.NodeID.ResolveNodeIdToIp().String()
			bpMGR.PendingUPF[curDPNodeIP] = true

			sessionContext, exist := smContext.PFCPContext[curDataPathNode.GetNodeIP()]
			if !exist || sessionContext.RemoteSEID == 0 {
				go establishPfcpSession(smContext, pfcpState, resChan)
			} else {
				go modifyExistingPfcpSession(smContext, pfcpState, resChan)
			}
		} else {
			if reflect.DeepEqual(curDataPathNode.UPF.NodeID, ulcl.NodeID) {
				nodeAfterULCL = true
			}
		}
	}

	bpMGR.AddingPSAState = context.EstablishingNewPSA
	// collect all responses
	for i := 0; i < len(bpMGR.PendingUPF); i++ {
		<-resChan
	}
	logger.PduSessLog.Traceln("End of EstablishPSA2")
}

func EstablishULCL(smContext *context.SMContext) {
	logger.PduSessLog.Infoln("In EstablishULCL")

	bpMGR := smContext.BPManager
	bpMGR.PendingUPF = make(context.PendingUPF)
	activatingPath := bpMGR.ActivatingPath
	dest := activatingPath.Destination
	ulcl := bpMGR.ULCL
	resChan := make(chan SendPfcpResult)

	// find updatedUPF in activatingPath
	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			UPLinkPDR := curDPNode.UpLinkTunnel.PDR
			DownLinkPDR := curDPNode.DownLinkTunnel.PDR
			UPLinkPDR.State = context.RULE_INITIAL

			FlowDespcription := flowdesc.NewIPFilterRule()
			err := FlowDespcription.SetAction(flowdesc.Permit) // permit
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDirection(flowdesc.Out) // uplink
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDestinationIP(dest.DestinationIP)
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetDestinationPorts(dest.DestinationPort)
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}
			err = FlowDespcription.SetSourceIP(smContext.PDUAddress.To4().String())
			if err != nil {
				logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
			}

			FlowDespcriptionStr, err := flowdesc.Encode(FlowDespcription)
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

			pfcpState := &PFCPState{
				upf:     ulcl,
				pdrList: []*context.PDR{UPLinkPDR, DownLinkPDR},
				farList: []*context.FAR{UPLinkPDR.FAR, DownLinkPDR.FAR},
				barList: []*context.BAR{},
				qerList: UPLinkPDR.QER,
			}

			curDPNodeIP := ulcl.NodeID.ResolveNodeIdToIp().String()
			bpMGR.PendingUPF[curDPNodeIP] = true
			go modifyExistingPfcpSession(smContext, pfcpState, resChan)
			break
		}
	}

	bpMGR.AddingPSAState = context.EstablishingULCL
	logger.PfcpLog.Info("[SMF] Establish ULCL msg has been send")

	// collect all responses
	for i := 0; i < len(bpMGR.PendingUPF); i++ {
		<-resChan
	}
}

func UpdatePSA2DownLink(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdatePSA2DownLink")

	bpMGR := smContext.BPManager
	bpMGR.PendingUPF = make(context.PendingUPF)
	ulcl := bpMGR.ULCL
	activatingPath := bpMGR.ActivatingPath
	resChan := make(chan SendPfcpResult)

	for curDataPathNode := activatingPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		lastNode := curDataPathNode.Prev()

		if lastNode != nil {
			if reflect.DeepEqual(lastNode.UPF.NodeID, ulcl.NodeID) {
				downLinkPDR := curDataPathNode.DownLinkTunnel.PDR
				downLinkPDR.State = context.RULE_INITIAL
				downLinkPDR.FAR.State = context.RULE_INITIAL

				qerList := []*context.QER{}
				qerList = append(qerList, downLinkPDR.QER...)
				pfcpState := &PFCPState{
					upf:     curDataPathNode.UPF,
					pdrList: []*context.PDR{downLinkPDR},
					farList: []*context.FAR{downLinkPDR.FAR},
					barList: []*context.BAR{},
					qerList: qerList,
				}

				curDPNodeIP := curDataPathNode.UPF.NodeID.ResolveNodeIdToIp().String()
				bpMGR.PendingUPF[curDPNodeIP] = true
				go modifyExistingPfcpSession(smContext, pfcpState, resChan)
				logger.PfcpLog.Info("[SMF] Update PSA2 downlink msg has been send")
				break
			}
		}
	}

	bpMGR.AddingPSAState = context.UpdatingPSA2DownLink

	// collect all responses
	for i := 0; i < len(bpMGR.PendingUPF); i++ {
		<-resChan
	}
}

func EstablishRANTunnelInfo(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In EstablishRANTunnelInfo")

	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath

	defaultPath := smContext.Tunnel.DataPathPool.GetDefaultPath()
	defaultANUPF := defaultPath.FirstDPNode

	activatingANUPF := activatingPath.FirstDPNode

	// Uplink ANUPF In TEID
	activatingANUPF.UpLinkTunnel.TEID = defaultANUPF.UpLinkTunnel.TEID
	activatingANUPF.UpLinkTunnel.PDR.PDI.LocalFTeid.Teid = defaultANUPF.UpLinkTunnel.PDR.PDI.LocalFTeid.Teid

	// Downlink ANUPF OutTEID

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
		NetworkInstance: &pfcpType.NetworkInstance{NetworkInstance: smContext.Dnn},
	}

	activatingANUPFDLFAR.State = context.RULE_INITIAL
	activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
	anOuterHeaderCreation := activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation
	anOuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
	anOuterHeaderCreation.Teid = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Teid
	anOuterHeaderCreation.Ipv4Address = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address
}

func UpdateRANAndIUPFUpLink(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdateRANAndIUPFUpLink")
	bpMGR := smContext.BPManager
	bpMGR.PendingUPF = make(context.PendingUPF)
	activatingPath := bpMGR.ActivatingPath
	dest := activatingPath.Destination
	ulcl := bpMGR.ULCL
	resChan := make(chan SendPfcpResult)

	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			break
		} else {
			UPLinkPDR := curDPNode.UpLinkTunnel.PDR
			DownLinkPDR := curDPNode.DownLinkTunnel.PDR
			UPLinkPDR.State = context.RULE_INITIAL
			DownLinkPDR.State = context.RULE_INITIAL

			if _, exist := bpMGR.UpdatedBranchingPoint[curDPNode.UPF]; exist {
				// add SDF Filter
				FlowDespcription := flowdesc.NewIPFilterRule()
				err := FlowDespcription.SetAction(flowdesc.Permit) // permit
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}
				err = FlowDespcription.SetDirection(flowdesc.Out) // uplink
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}
				err = FlowDespcription.SetDestinationIP(dest.DestinationIP)
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}
				err = FlowDespcription.SetDestinationPorts(dest.DestinationPort)
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}
				err = FlowDespcription.SetSourceIP(smContext.PDUAddress.To4().String())
				if err != nil {
					logger.PduSessLog.Errorf("Error occurs when setting flow despcription: %s\n", err)
				}

				FlowDespcriptionStr, err := flowdesc.Encode(FlowDespcription)
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

			pfcpState := &PFCPState{
				upf:     curDPNode.UPF,
				pdrList: []*context.PDR{UPLinkPDR, DownLinkPDR},
				farList: []*context.FAR{UPLinkPDR.FAR, DownLinkPDR.FAR},
				barList: []*context.BAR{},
				qerList: UPLinkPDR.QER,
			}

			curDPNodeIP := curDPNode.UPF.NodeID.ResolveNodeIdToIp().String()
			bpMGR.PendingUPF[curDPNodeIP] = true
			go modifyExistingPfcpSession(smContext, pfcpState, resChan)
		}
	}

	if !bpMGR.PendingUPF.IsEmpty() {
		bpMGR.AddingPSAState = context.UpdatingRANAndIUPFUpLink
		// collect all responses
		for i := 0; i < len(bpMGR.PendingUPF); i++ {
			<-resChan
		}
	}
	bpMGR.AddingPSAState = context.Finished
	bpMGR.BPStatus = context.AddPSASuccess
	logger.CtxLog.Infoln("[SMF] Add PSA success")
}
