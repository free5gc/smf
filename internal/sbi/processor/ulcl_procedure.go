package processor

import (
	"reflect"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/context"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/flowdesc"
	"github.com/google/uuid"
)

func (p *Processor) AddPDUSessionAnchorAndULCL(smContext *smf_context.SMContext) {
	smContext.Log.Traceln("In AddPDUSessionAnchorAndULCL")
	bpMGR := smContext.BPManager

	switch bpMGR.AddingPSAState {
	case smf_context.ActivatingDataPath:
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
			p.EstablishPSA2(smContext)

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

func (p *Processor) EstablishPSA2(smContext *smf_context.SMContext) {
	smContext.Log.Infoln("Establish PSA2")
	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	ulcl := bpMGR.ULCL
	nodeAfterULCL := false

	resChan := make(chan context.SendPfcpResult)
	defer close(resChan)

	pendingUPFs := []uuid.UUID{}

	for node := activatingPath.FirstDPNode; node != nil; node = node.Next() {
		upf := node.UPF
		if nodeAfterULCL {
			addr := upf.PFCPAddr()

			logger.PduSessLog.Tracef("Send to upf addr %s", addr)

			chgUrrList := []*context.URR{}
			for _, urr := range node.UpLinkTunnel.PDR.URR {
				if urr.ReportingTrigger.Start {
					chgUrrList = append(chgUrrList, urr)
				}
			}

			// According to 32.255 5.2.2.7, Addition of additional PDU Session Anchor is a charging event
			p.UpdateChargingSession(smContext, chgUrrList, models.Trigger{
				TriggerType:     models.TriggerType_ADDITION_OF_UPF,
				TriggerCategory: models.TriggerCategory_IMMEDIATE_REPORT,
			})

			pendingUPFs = append(pendingUPFs, node.UPF.ID)

			pfcpSessionContext, exist := smContext.PFCPSessionContexts[node.UPF.ID]
			if !exist || pfcpSessionContext.RemoteSEID == 0 {
				go establishPfcpSession(pfcpSessionContext, resChan)
			} else {
				go modifyExistingPfcpSession(smContext, pfcpSessionContext, resChan, "")
			}
		} else if reflect.DeepEqual(node.UPF.NodeID, ulcl.NodeID) {
			nodeAfterULCL = true
		}
	}

	bpMGR.AddingPSAState = context.EstablishingNewPSA

	waitAllPfcpRsp(len(pendingUPFs), context.SessionEstablishSuccess, context.SessionEstablishFailed, resChan)

	// TODO: remove failed PSA2 path
	logger.PduSessLog.Traceln("End of EstablishPSA2")
}

func EstablishULCL(smContext *context.SMContext) {
	logger.PduSessLog.Infoln("In EstablishULCL")

	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	dest := activatingPath.Destination
	ulcl := bpMGR.ULCL

	resChan := make(chan context.SendPfcpResult)
	defer close(resChan)

	pendingUPFs := []uuid.UUID{}

	// find updatedUPF in activatingPath
	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			UPLinkPDR := curDPNode.UpLinkTunnel.PDR
			UPLinkPDR.SetState(context.RULE_INITIAL)

			// new IPFilterRule with action:"permit" and diection:"out"
			FlowDespcription := flowdesc.NewIPFilterRule()
			FlowDespcription.Dst = dest.DestinationIP
			if dstPort, err := flowdesc.ParsePorts(dest.DestinationPort); err != nil {
				FlowDespcription.DstPorts = dstPort
			}
			FlowDespcription.Src = smContext.PDUAddress.To4().String()

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

			pendingUPFs = append(pendingUPFs, ulcl.ID)
			go modifyExistingPfcpSession(smContext, smContext.PFCPSessionContexts[ulcl.ID], resChan, "")
			break
		}
	}

	bpMGR.AddingPSAState = context.EstablishingULCL
	logger.PfcpLog.Info("[SMF] Establish ULCL msg has been send")

	waitAllPfcpRsp(len(pendingUPFs), context.SessionEstablishSuccess, context.SessionEstablishFailed, resChan)
}

func UpdatePSA2DownLink(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdatePSA2DownLink")

	bpMGR := smContext.BPManager
	ulcl := bpMGR.ULCL
	activatingPath := bpMGR.ActivatingPath

	resChan := make(chan context.SendPfcpResult)
	defer close(resChan)

	pendingUPFs := []uuid.UUID{}

	for node := activatingPath.FirstDPNode; node != nil; node = node.Next() {
		lastNode := node.Prev()

		if lastNode != nil {
			if reflect.DeepEqual(lastNode.UPF.NodeID, ulcl.NodeID) {
				downLinkPDR := node.DownLinkTunnel.PDR
				downLinkPDR.SetState(context.RULE_INITIAL)
				downLinkPDR.FAR.SetState(context.RULE_INITIAL)

				pendingUPFs = append(pendingUPFs, node.UPF.ID)
				go modifyExistingPfcpSession(smContext, smContext.PFCPSessionContexts[node.UPF.ID], resChan, "")
				logger.PfcpLog.Info("[SMF] Update PSA2 downlink msg has been sent")
				break
			}
		}
	}

	bpMGR.AddingPSAState = context.UpdatingPSA2DownLink

	waitAllPfcpRsp(len(pendingUPFs), context.SessionUpdateSuccess, context.SessionEstablishFailed, resChan)
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
		NetworkInstance: &pfcpType.NetworkInstance{
			NetworkInstance: smContext.Dnn,
			FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
		},
	}

	activatingANUPFDLFAR.SetState(context.RULE_INITIAL)
	activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)
	anOuterHeaderCreation := activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation
	anOuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
	anOuterHeaderCreation.Teid = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Teid
	anOuterHeaderCreation.Ipv4Address = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address
}

func UpdateRANAndIUPFUpLink(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdateRANAndIUPFUpLink")
	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	dest := activatingPath.Destination
	ulcl := bpMGR.ULCL

	resChan := make(chan context.SendPfcpResult)
	defer close(resChan)

	pendingUPFs := []uuid.UUID{}

	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			break
		} else {
			UPLinkPDR := curDPNode.UpLinkTunnel.PDR
			DownLinkPDR := curDPNode.DownLinkTunnel.PDR
			UPLinkPDR.SetState(context.RULE_INITIAL)
			DownLinkPDR.SetState(context.RULE_INITIAL)

			if _, exist := bpMGR.UpdatedBranchingPoint[curDPNode.UPF]; exist {
				// add SDF Filter
				// new IPFilterRule with action:"permit" and diection:"out"
				FlowDespcription := flowdesc.NewIPFilterRule()
				FlowDespcription.Dst = dest.DestinationIP
				if dstPort, err := flowdesc.ParsePorts(dest.DestinationPort); err != nil {
					FlowDespcription.DstPorts = dstPort
				}
				FlowDespcription.Src = smContext.PDUAddress.To4().String()

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

			pendingUPFs = append(pendingUPFs, curDPNode.UPF.ID)
			go modifyExistingPfcpSession(smContext, smContext.PFCPSessionContexts[curDPNode.UPF.ID], resChan, "")
		}
	}

	if len(pendingUPFs) > 0 {
		bpMGR.AddingPSAState = context.UpdatingRANAndIUPFUpLink
		waitAllPfcpRsp(len(pendingUPFs), context.SessionUpdateSuccess, context.SessionEstablishFailed, resChan)
	}
	bpMGR.AddingPSAState = context.Finished
	bpMGR.BPStatus = context.AddPSASuccess
	logger.CtxLog.Infoln("[SMF] Add PSA success")
}
