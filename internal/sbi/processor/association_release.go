package processor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
)

type associationReleaseSessionDeletionClient interface {
	SendSessionDeletionRequest(
		context.Context, *message.SessionDeletionRequest, *net.UDPAddr, uint64,
	) (*message.SessionDeletionResponse, error)
}

type associationReleasePDUSessionResult struct {
	smContext *smf_context.SMContext
	deleted   int
	attempted int
	err       error
}

// deletePFCPSessionsBeforeAssociationRelease terminates every PDU Session that
// uses the affected UPF. Each worker owns one complete PDU Session: it deletes
// all of that PDU Session's PFCP sessions, collects final Usage Reports, and
// only then starts charging and AMF-side termination. This keeps the number of
// active PFCP/SBI operations bounded when one UPF owns many sessions.
func (p *Processor) deletePFCPSessionsBeforeAssociationRelease(ctx context.Context, upf *smf_context.UPF) {
	if err := ctx.Err(); err != nil {
		logger.PfcpLog.Warnf(
			"PDU Session termination before Association Release for UPF%s expired before collecting targets: %v",
			formatUPF(upf), err,
		)
		return
	}
	targets := upf.CollectAssociationReleasePDUSessions()
	if len(targets) == 0 {
		logger.PfcpLog.Infof("UPF%s has no PDU Sessions to terminate before Association Release", formatUPF(upf))
		return
	}
	workers := p.associationReleaseWorkerCount()
	if workers > len(targets) {
		workers = len(targets)
	}

	jobs := make(chan *smf_context.SMContext, len(targets))
	results := make(chan associationReleasePDUSessionResult, len(targets))
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)
	for worker := 0; worker < workers; worker++ {
		go func() {
			for smContext := range jobs {
				deleted, attempted, err := p.terminateAssociationAffectedPDUSession(ctx, upf, smContext)
				results <- associationReleasePDUSessionResult{
					smContext: smContext,
					deleted:   deleted,
					attempted: attempted,
					err:       err,
				}
			}
		}()
	}

	failed := 0
	for range targets {
		result := <-results
		if result.err != nil {
			failed++
			logger.PfcpLog.Warnf(
				"terminate PDU Session before Association Release ref=%s on UPF%s: deleted PFCP sessions=%d/%d: %v",
				result.smContext.Ref, formatUPF(upf), result.deleted, result.attempted, result.err,
			)
		}
	}
	logger.PfcpLog.Infof(
		"PDU Session termination before Association Release for UPF%s completed: sessions=%d completed=%d incomplete=%d",
		formatUPF(upf), len(targets), len(targets)-failed, failed,
	)
}

func associationReleaseContext(
	parent context.Context,
	period *time.Duration,
) (context.Context, context.CancelFunc) {
	if period == nil || *period == time.Duration(math.MaxInt64) {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, *period)
}

func (p *Processor) associationReleaseWorkerCount() int {
	if p != nil && p.ProcessorSmf != nil && p.Config() != nil {
		return p.Config().GetPfcpAssociationReleaseWorkerCount()
	}
	return factory.PfcpDefaultAssociationReleaseWorkers
}

func (p *Processor) terminateAssociationAffectedPDUSession(
	ctx context.Context,
	releasingUPF *smf_context.UPF,
	smContext *smf_context.SMContext,
) (deleted int, attempted int, resultErr error) {
	if releasingUPF == nil || smContext == nil {
		return 0, 0, fmt.Errorf("invalid PDU Session termination target")
	}

	// Serialize the entire PDU Session lifecycle with UE/AMF-triggered release.
	// Mutable PFCP contexts are read only after this lock is held, so queued work
	// cannot act on an old Remote SEID.
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	if !pduSessionUsesUPF(smContext, releasingUPF) {
		return 0, 0, nil
	}

	var deletionErrors []error
	for _, sessionContext := range smContext.PFCPContext {
		if sessionContext == nil || sessionContext.RemoteSEID == 0 {
			continue
		}
		attempted++
		sessionUPF := smf_context.RetrieveUPFNodeByNodeID(sessionContext.NodeID)
		if sessionUPF == nil {
			deletionErrors = append(deletionErrors,
				fmt.Errorf("PFCP session localSEID=%d refers to unknown UPF %s",
					sessionContext.LocalSEID, sessionContext.NodeID.String()))
			continue
		}
		if err := p.deleteAssociationPFCPSession(ctx, sessionUPF, smContext, sessionContext); err != nil {
			deletionErrors = append(deletionErrors, fmt.Errorf(
				"delete PFCP session localSEID=%d remoteSEID=%d on UPF%s: %w",
				sessionContext.LocalSEID, sessionContext.RemoteSEID, formatUPF(sessionUPF), err))
			continue
		}
		deleted++
	}

	// This is a forced PDU Session termination. PFCP transaction errors are
	// reported, but they must not leave charging and AMF resources alive after
	// the UPF association is gone. Available final reports are consumed first.
	// A later AMF release can retry only the PFCP sessions that did not complete.
	smContext.PFCPReleaseDone = len(deletionErrors) == 0
	p.finalizeAssociationAffectedPDUSession(smContext)
	return deleted, attempted, errors.Join(deletionErrors...)
}

func pduSessionUsesUPF(smContext *smf_context.SMContext, upf *smf_context.UPF) bool {
	for _, sessionContext := range smContext.PFCPContext {
		if sessionContext != nil && sessionContext.RemoteSEID != 0 &&
			sessionContext.NodeID.EqualsTo(&upf.NodeID) {
			return true
		}
	}
	return false
}

func (p *Processor) deleteAssociationPFCPSession(
	ctx context.Context,
	upf *smf_context.UPF,
	smContext *smf_context.SMContext,
	sessionContext *smf_context.PFCPSessionContext,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := upf.IsAssociated(); err != nil {
		return err
	}
	client, ok := p.getActivePFCPClient().(associationReleaseSessionDeletionClient)
	if !ok {
		return fmt.Errorf("active PFCP client does not support context-aware Session Deletion")
	}

	request := message.NewSessionDeletionRequest(0, 0, sessionContext.RemoteSEID, 0, 0)
	response, err := client.SendSessionDeletionRequest(
		ctx,
		request,
		&net.UDPAddr{IP: upf.NodeID.ResolveNodeIdToIp(), Port: pfcpPeerPort},
		sessionContext.LocalSEID,
	)
	if err != nil {
		return err
	}
	if response == nil || response.Cause == nil {
		return fmt.Errorf("PFCP Session Deletion Response is missing Cause")
	}
	cause, err := response.Cause.Cause()
	if err != nil {
		return fmt.Errorf("decode PFCP Session Deletion Cause: %w", err)
	}
	if cause != ie.CauseRequestAccepted {
		return fmt.Errorf("PFCP Session Deletion rejected with Cause %d", cause)
	}

	// Accepted means the UPF session is gone even when a report is malformed.
	// Clear first so no later release path sends the same deletion again.
	sessionContext.RemoteSEID = 0
	if len(response.UsageReport) == 0 {
		return nil
	}
	if err = smContext.HandleReports(response.UsageReport, upf.NodeID, ""); err != nil {
		return fmt.Errorf("decode final Usage Report: %w", err)
	}
	return nil
}

func (p *Processor) finalizeAssociationAffectedPDUSession(smContext *smf_context.SMContext) {
	if p == nil || p.ProcessorSmf == nil {
		// Unit tests may construct a Processor without the service/consumer.
		logger.PfcpLog.Warnf(
			"skip NF-side PDU Session finalization for SMContext[%s]: processor service is not configured",
			smContext.Ref,
		)
		return
	}

	p.ReleaseChargingSession(smContext)
	sendNotification, removeContext := p.requestAMFToReleasePDUResources(smContext)
	if sendNotification {
		p.SendReleaseNotification(smContext)
	}
	if removeContext {
		// requestAMFToReleasePDUResources already sent the required notification.
		p.RemoveSMContextFromAllNF(smContext, false)
	}
}
