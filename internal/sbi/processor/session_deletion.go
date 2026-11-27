package processor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
)

const (
	maxPFCPDeletionAttempts = 3
	// The paired go-upf holds a completed response and its SEID for one minute.
	pfcpDeletionDeadline   = 45 * time.Second
	pfcpDeletionRetryDelay = time.Second
)

func (p *Processor) requestSessionDeletion(
	ctx context.Context, upf *smf_context.UPF, localSEID, remoteSEID uint64,
) (*message.SessionDeletionResponse, error) {
	client, ok := p.getActivePFCPClient().(SessionDeletionPFCPClient)
	if !ok {
		return nil, fmt.Errorf("go-pfcp Session Deletion client is not configured")
	}
	peer := &net.UDPAddr{IP: upf.NodeID.ResolveNodeIdToIp(), Port: pfcpPeerPort}
	associationContext, err := upf.AssociationContext()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pfcpDeletionDeadline)
	defer cancel()
	stop := context.AfterFunc(associationContext, cancel)
	defer stop()
	return sendSessionDeletion(ctx, client, peer, localSEID, remoteSEID, pfcpDeletionRetryDelay)
}

func sendSessionDeletion(
	ctx context.Context, client SessionDeletionPFCPClient, peer *net.UDPAddr,
	localSEID, remoteSEID uint64, retryDelay time.Duration,
) (*message.SessionDeletionResponse, error) {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Each attempt gets a new sequence from the PFCP transaction layer.
		request := message.NewSessionDeletionRequest(0, 0, remoteSEID, 0, 0)
		response, err := client.SendSessionDeletionRequest(ctx, request, peer, localSEID)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		retry := errors.Is(err, context.DeadlineExceeded)
		if err == nil {
			if response == nil || response.Cause == nil {
				return nil, fmt.Errorf("PFCP Session Deletion Response is missing Cause")
			}
			cause, causeErr := response.Cause.Cause()
			if causeErr != nil {
				return nil, fmt.Errorf("decode PFCP Session Deletion Cause: %w", causeErr)
			}
			if cause == ie.CauseRequestAccepted {
				return response, nil
			}
			err = fmt.Errorf("PFCP Session Deletion rejected with Cause %d", cause)
			retry = cause == ie.CauseSystemFailure
		}
		if !retry || attempt == maxPFCPDeletionAttempts {
			return nil, err
		}

		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
