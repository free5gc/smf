package processor

import (
	"context"
	"net"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

// SetupAssociation validates a passive setup against the configured UPF and
// defers recovery-time handling until its response has been sent. A newer
// timestamp invalidates the current generation; the lifecycle owner performs
// session cleanup and re-establishment after its association context closes.
func (p *Processor) SetupAssociation(
	peer pfcptype.NodeID,
	recoveryTime time.Time,
) (uint8, func()) {
	upf := smf_context.RetrieveUPFNodeByNodeID(peer)
	if upf == nil {
		return ie.CauseRequestRejected, nil
	}
	_, generation := upf.AssociationStateAndGeneration()
	return ie.CauseRequestAccepted, func() {
		state, currentGeneration := upf.AssociationStateAndGeneration()
		if currentGeneration != generation {
			return
		}
		current, restarted := upf.AcceptRecoveryTimeStampForGeneration(generation, recoveryTime)
		if !current || !restarted {
			return
		}
		if state != smf_context.AssociationEstablished && state != smf_context.AssociationReleasing {
			return
		}
		logger.PfcpLog.Warnf(
			"UPF[%s] sent Association Setup with a newer Recovery Time Stamp; invalidating association generation %d",
			peer.String(), generation,
		)
		upf.CancelAssociationIfGeneration(generation)
	}
}

// UpdateAssociation prepares changes only for an established association.
// Feature updates and release work commit after the Update Response is sent.
func (p *Processor) UpdateAssociation(
	peer pfcptype.NodeID,
	request *message.AssociationUpdateRequest,
) (uint8, func()) {
	if request == nil {
		return ie.CauseRequestRejected, nil
	}
	upf := smf_context.RetrieveUPFNodeByNodeID(peer)
	if upf == nil || upf.IsAssociated() != nil {
		return ie.CauseNoEstablishedPFCPAssociation, nil
	}
	var features []byte
	if request.UPFunctionFeatures != nil {
		parsed, err := request.UPFunctionFeatures.UPFunctionFeatures()
		if err != nil || len(parsed) < 2 {
			return ie.CauseInvalidLength, nil
		}
		features = append([]byte(nil), parsed...)
	}
	if request.PFCPAUReqFlags != nil && request.PFCPAUReqFlags.HasPARPS() {
		logger.PfcpLog.Warnf(
			"UPF[%s] requested enhanced PFCP association release preparation, but SMF does not advertise EPFAR",
			peer.String(),
		)
	}

	releaseRequest := request.PFCPAssociationReleaseRequest
	releaseRequested := releaseRequest != nil && releaseRequest.HasSARR()
	if releaseRequest != nil && releaseRequest.HasURSS() {
		logger.PfcpLog.Infof(
			"UPF[%s] reported that non-zero Usage Reports for affected PFCP Sessions were sent",
			peer.String(),
		)
	}
	// URSS reports what the UPF has already sent; only SARR requests release.
	var gracefulReleasePeriod *time.Duration
	if request.GracefulReleasePeriod != nil {
		period, err := request.GracefulReleasePeriod.GracefulReleasePeriod()
		if err != nil {
			return ie.CauseInvalidLength, nil
		}
		gracefulReleasePeriod = &period
	}
	if len(features) == 0 && !releaseRequested {
		return ie.CauseRequestAccepted, nil
	}
	return ie.CauseRequestAccepted, func() {
		if len(features) != 0 {
			upf.SetUPFunctionFeatures(features)
		}
		if releaseRequested {
			p.releaseAssociationRequestedByUPF(upf, gracefulReleasePeriod)
		}
	}
}

type associationReleasePFCPClient interface {
	SendAssociationReleaseRequest(
		context.Context, *net.UDPAddr,
	) (*message.AssociationReleaseResponse, error)
}

func (p *Processor) releaseAssociationRequestedByUPF(
	upf *smf_context.UPF,
	gracefulReleasePeriod *time.Duration,
) {
	upfString := formatUPF(upf)
	pfcpContext := smf_context.GetSelf().PfcpContext
	if pfcpContext == nil {
		logger.PfcpLog.Errorf("cannot release association to UPF%s: SMF PFCP context is not configured", upfString)
		cancelUPFAssociation(upf)
		return
	}
	// The UPF-provided graceful period covers the complete preparation phase,
	// including waiting for PFCP session work that was already in flight.
	releaseContext, cancelRelease := associationReleaseContext(pfcpContext, gracefulReleasePeriod)
	defer cancelRelease()
	if !upf.BeginAssociationRelease() {
		logger.PfcpLog.Infof("PFCP Association Release for UPF%s is already in progress", upfString)
		return
	}
	associationContext, err := upf.AssociationContext()
	if err != nil {
		logger.PfcpLog.Errorf("cannot release association to UPF%s: %v", upfString, err)
		cancelUPFAssociation(upf)
		return
	}

	p.deletePFCPSessionsBeforeAssociationRelease(releaseContext, upf)

	client, ok := p.getActivePFCPClient().(associationReleasePFCPClient)
	if !ok {
		logger.PfcpLog.Errorf(
			"cannot release association to UPF%s: active PFCP client does not support Association Release",
			upfString,
		)
		cancelUPFAssociation(upf)
		return
	}
	_, err = client.SendAssociationReleaseRequest(associationContext, &net.UDPAddr{
		IP:   upf.NodeID.ResolveNodeIdToIp(),
		Port: pfcpPeerPort,
	})
	if err != nil {
		logger.PfcpLog.Errorf("PFCP Association Release Request to UPF%s failed: %v", upfString, err)
	}
	// A lost response leaves the peer's state ambiguous. Invalidate the local
	// association on both success and failure so no new sessions use this UPF.
	cancelUPFAssociation(upf)
}

// ReleaseAssociation invalidates the existing association without deleting the
// configured UPF topology. The active state machine performs session cleanup
// and decides whether to establish the configured association again.
func (p *Processor) ReleaseAssociation(peer pfcptype.NodeID) uint8 {
	upf := smf_context.RetrieveUPFNodeByNodeID(peer)
	if upf == nil {
		return ie.CauseNoEstablishedPFCPAssociation
	}
	cancelUPFAssociation(upf)
	return ie.CauseRequestAccepted
}
