package processor

import (
	"net"
	"reflect"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

func (p *Processor) AddPDUSessionAnchorAndULCL(smContext *context.SMContext) error {
	smContext.Log.Infoln("In AddPDUSessionAnchorAndULCL")
	bpMGR := smContext.BPManager

	switch bpMGR.AddingPSAState {
	case context.ActivatingDataPath:
		for {
			bpMGR.ActivatingPath = nil
			// select PSA2
			bpMGR.SelectPSA2(smContext)
			if bpMGR.ActivatingPath == nil {
				smContext.Log.Infoln("No more valid PSA2 path found, finish selection.")
				break
			}
			smContext.AllocateLocalSEIDForDataPath(bpMGR.ActivatingPath)
			// select an upf as ULCL
			err := bpMGR.FindULCL(smContext)
			if err != nil {
				return err
			}

			// Allocate Path PDR and TEID
			bindings, err := bpMGR.ActivatingPath.ULCLQoSBindings(smContext)
			if err != nil {
				return err
			}
			if err = bpMGR.ActivatingPath.ActivateTunnelAndPDR(smContext, 255, bindings...); err != nil {
				return err
			}
			// N1N2MessageTransfer Here

			// Establish PSA2
			p.EstablishPSA2(smContext)

			EstablishRANTunnelInfo(smContext)
			// Establish ULCL
			err = p.EstablishULCL(smContext)
			if err != nil {
				return err
			}

			p.UpdatePSA2DownLink(smContext)

			p.UpdateRANAndIUPFUpLink(smContext)
		}
	default:
		logger.CtxLog.Warnln("unexpected status")
	}
	return nil
}

func (p *Processor) EstablishPSA2(smContext *context.SMContext) {
	smContext.Log.Infoln("Establish PSA2")
	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	ulcl := bpMGR.ULCL
	nodeAfterULCL := false
	resChan := make(chan SendPfcpResult)
	pendingUPFs := []string{}

	for node := activatingPath.FirstDPNode; node != nil; node = node.Next() {
		if nodeAfterULCL {
			addr := net.UDPAddr{
				IP:   context.ResolveIP(node.UPF.Addr),
				Port: pfcpPeerPort,
			}

			logger.PduSessLog.Traceln("Send to upf addr: ", addr.String())

			tunnels := []*context.GTPTunnel{node.UpLinkTunnel}
			lastNode := node.Prev()
			if lastNode != nil && !reflect.DeepEqual(lastNode.UPF.NodeID, ulcl.NodeID) {
				tunnels = append(tunnels, node.DownLinkTunnel)
			}
			pfcpState := pfcpStateForTunnels(node.UPF, tunnels...)

			chgUrrList := []*context.URR{}
			for _, urr := range pfcpState.urrList {
				if urr.ReportingTrigger.HasSTART() {
					chgUrrList = append(chgUrrList, urr)
				}
			}

			// According to 32.255 5.2.2.7, Addition of additional PDU Session Anchor is a charging event
			if len(chgUrrList) != 0 {
				p.UpdateChargingSession(smContext, chgUrrList, models.Chf_ConvCharging_Trigger{
					TriggerType:     models.Chf_ConvCharging_TriggerType_ADDITION_OF_UPF,
					TriggerCategory: models.Chf_ConvCharging_TriggerCategory_IMMEDIATE_REPORT,
				})
			}

			curDPNodeIP := node.UPF.NodeID.ResolveNodeIdToIp().String()
			pendingUPFs = append(pendingUPFs, curDPNodeIP)

			sessionContext, exist := smContext.PFCPContext[node.GetNodeIP()]
			if !exist || sessionContext.RemoteSEID == 0 {
				go p.establishPfcpSession(smContext, pfcpState, resChan)
			} else {
				go p.modifyExistingPfcpSession(smContext, pfcpState, resChan, "")
			}
		} else if reflect.DeepEqual(node.UPF.NodeID, ulcl.NodeID) {
			nodeAfterULCL = true
		}
	}

	bpMGR.AddingPSAState = context.EstablishingNewPSA

	waitAllPfcpRsp(smContext, len(pendingUPFs), resChan, nil)
	close(resChan)
	// TODO: remove failed PSA2 path
	logger.PduSessLog.Traceln("End of EstablishPSA2")
}

func (p *Processor) EstablishULCL(smContext *context.SMContext) error {
	logger.PduSessLog.Infoln("In EstablishULCL")

	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	ulcl := bpMGR.ULCL
	resChan := make(chan SendPfcpResult)
	pendingUPFs := []string{}

	// find updatedUPF in activatingPath
	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		smContext.Log.Traceln("Current DP Node IP: ", curDPNode.UPF.NodeID.ResolveNodeIdToIp().String())

		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			pfcpState := pfcpStateForTunnels(ulcl, curDPNode.UpLinkTunnel, curDPNode.DownLinkTunnel)

			curDPNodeIP := ulcl.NodeID.ResolveNodeIdToIp().String()
			pendingUPFs = append(pendingUPFs, curDPNodeIP)
			go p.modifyExistingPfcpSession(smContext, pfcpState, resChan, "")
			break
		}
	}

	bpMGR.AddingPSAState = context.EstablishingULCL
	logger.PfcpLog.Info("[SMF] Establish ULCL msg has been send")

	waitAllPfcpRsp(smContext, len(pendingUPFs), resChan, nil)
	close(resChan)
	return nil
}

func (p *Processor) UpdatePSA2DownLink(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdatePSA2DownLink")

	bpMGR := smContext.BPManager
	ulcl := bpMGR.ULCL
	activatingPath := bpMGR.ActivatingPath
	resChan := make(chan SendPfcpResult)
	pendingUPFs := []string{}

	for node := activatingPath.FirstDPNode; node != nil; node = node.Next() {
		lastNode := node.Prev()

		if lastNode != nil {
			if reflect.DeepEqual(lastNode.UPF.NodeID, ulcl.NodeID) {
				for _, pdr := range node.DownLinkTunnel.PDRs() {
					pdr.State, pdr.FAR.State = context.RULE_INITIAL, context.RULE_INITIAL
				}
				pfcpState := pfcpStateForTunnels(node.UPF, node.DownLinkTunnel)

				curDPNodeIP := node.UPF.NodeID.ResolveNodeIdToIp().String()
				pendingUPFs = append(pendingUPFs, curDPNodeIP)
				go p.modifyExistingPfcpSession(smContext, pfcpState, resChan, "")
				logger.PfcpLog.Info("[SMF] Update PSA2 downlink msg has been send")
				break
			}
		}
	}

	bpMGR.AddingPSAState = context.UpdatingPSA2DownLink

	waitAllPfcpRsp(smContext, len(pendingUPFs), resChan, nil)
	close(resChan)
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
	activatingANUPFDLFAR.ApplyAction = pfcptype.ApplyAction{
		Buff: false,
		Drop: false,
		Dupl: false,
		Forw: true,
		Nocp: false,
	}
	activatingANUPFDLFAR.ForwardingParameters = &context.ForwardingParameters{
		DestinationInterface: pfcptype.DestinationInterface{
			InterfaceValue: pfcptype.DestinationInterfaceAccess,
		},
		NetworkInstance: &pfcptype.NetworkInstance{
			NetworkInstance: smContext.Dnn,
			FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
		},
	}

	activatingANUPFDLFAR.State = context.RULE_INITIAL
	activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation = new(pfcptype.OuterHeaderCreation)
	anOuterHeaderCreation := activatingANUPFDLFAR.ForwardingParameters.OuterHeaderCreation
	anOuterHeaderCreation.OuterHeaderCreationDescription = pfcptype.OuterHeaderCreationGtpUUdpIpv4
	anOuterHeaderCreation.Teid = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Teid
	anOuterHeaderCreation.Ipv4Address = defaultANUPFDLFAR.ForwardingParameters.OuterHeaderCreation.Ipv4Address
}

func (p *Processor) UpdateRANAndIUPFUpLink(smContext *context.SMContext) {
	logger.PduSessLog.Traceln("In UpdateRANAndIUPFUpLink")
	bpMGR := smContext.BPManager
	activatingPath := bpMGR.ActivatingPath
	ulcl := bpMGR.ULCL
	resChan := make(chan SendPfcpResult)
	pendingUPFs := []string{}

	for curDPNode := activatingPath.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if reflect.DeepEqual(ulcl.NodeID, curDPNode.UPF.NodeID) {
			break
		} else {
			for _, tunnel := range []*context.GTPTunnel{curDPNode.UpLinkTunnel, curDPNode.DownLinkTunnel} {
				for _, pdr := range tunnel.PDRs() {
					pdr.State = context.RULE_INITIAL
				}
			}
			pfcpState := pfcpStateForTunnels(curDPNode.UPF, curDPNode.UpLinkTunnel, curDPNode.DownLinkTunnel)

			curDPNodeIP := curDPNode.UPF.NodeID.ResolveNodeIdToIp().String()
			pendingUPFs = append(pendingUPFs, curDPNodeIP)
			go p.modifyExistingPfcpSession(smContext, pfcpState, resChan, "")
		}
	}

	if len(pendingUPFs) > 0 {
		bpMGR.AddingPSAState = context.UpdatingRANAndIUPFUpLink
		waitAllPfcpRsp(smContext, len(pendingUPFs), resChan, nil)
		close(resChan)
	}
	bpMGR.AddingPSAState = context.Finished
	bpMGR.BPStatus = context.AddPSASuccess
	logger.CtxLog.Infoln("[SMF] Add PSA success")
}
