package processor

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func TestDeletePFCPSessionRetriesAndAccountsUsageOnce(t *testing.T) {
	nodeID := pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.IPv4(192, 0, 2, 201).To4()}
	upf := smf_context.NewUPF(&nodeID, nil)
	upf.EstablishAssociation(context.Background())
	t.Cleanup(func() {
		upf.CancelAssociation()
		smf_context.RemoveUPFNodeByNodeID(nodeID)
	})
	session := &smf_context.PFCPSessionContext{NodeID: nodeID, LocalSEID: 101, RemoteSEID: 201}
	smContext := &smf_context.SMContext{PFCPContext: map[string]*smf_context.PFCPSessionContext{nodeID.String(): session}}
	p := &Processor{}
	var requests []*message.SessionDeletionRequest
	p.SetActivePFCPClient(&fakeActivePFCPClient{
		deleteCtx: func(_ context.Context, req *message.SessionDeletionRequest, addr *net.UDPAddr,
			localSEID uint64,
		) (*message.SessionDeletionResponse, error) {
			requests = append(requests, req)
			require.Equal(t, uint64(201), req.SEID())
			require.Equal(t, uint64(101), localSEID)
			require.Equal(t, nodeID.String(), addr.IP.String())
			require.Equal(t, uint64(201), session.RemoteSEID)
			if len(requests) == 1 {
				return message.NewSessionDeletionResponse(0, 0, localSEID, 1, 0, ie.NewCause(ie.CauseSystemFailure)), nil
			}
			return message.NewSessionDeletionResponse(0, 0, localSEID, 2, 0,
				ie.NewCause(ie.CauseRequestAccepted),
				ie.NewUsageReportWithinSessionDeletionResponse(
					ie.NewURRID(3), ie.NewUsageReportTrigger(0, 0x08),
					ie.NewVolumeMeasurement(0x07, 42, 42, 0, 0, 0, 0),
				),
			), nil
		},
	})
	results := make(chan SendPfcpResult, 1)
	p.deletePfcpSession(upf, smContext, results)
	require.Equal(t, SendPfcpResult{Status: smf_context.SessionReleaseSuccess}, <-results)
	require.Len(t, requests, 2)
	require.NotSame(t, requests[0], requests[1])
	require.Zero(t, session.RemoteSEID)
	require.Len(t, smContext.UrrReports, 1)
	require.EqualValues(t, 42, smContext.UrrReports[0].TotalVolume)

	p.deletePfcpSession(upf, smContext, results)
	require.Equal(t, SendPfcpResult{Status: smf_context.SessionReleaseSuccess}, <-results)
	require.Len(t, requests, 2)
	require.Len(t, smContext.UrrReports, 1)
}

func TestSessionDeletionRetryLimitAndStopConditions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cause   uint8
		sendErr error
		cancel  bool
		want    int
	}{
		{name: "three system failures", cause: ie.CauseSystemFailure, want: 3},
		{name: "wrong session", cause: ie.CauseSessionContextNotFound, want: 1},
		{name: "transport timeout", sendErr: context.DeadlineExceeded, want: 3},
		{name: "association canceled", cause: ie.CauseSystemFailure, cancel: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client := &fakeActivePFCPClient{
				deleteCtx: func(_ context.Context, _ *message.SessionDeletionRequest, _ *net.UDPAddr,
					localSEID uint64,
				) (*message.SessionDeletionResponse, error) {
					calls++
					if tc.cancel {
						cancel()
					}
					if tc.sendErr != nil {
						return nil, tc.sendErr
					}
					return message.NewSessionDeletionResponse(0, 0, localSEID, 1, 0, ie.NewCause(tc.cause)), nil
				},
			}
			rsp, err := sendSessionDeletion(ctx, client, &net.UDPAddr{}, 101, 201, time.Nanosecond)
			require.Error(t, err)
			require.Nil(t, rsp)
			require.Equal(t, tc.want, calls)
			if tc.cancel {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}
