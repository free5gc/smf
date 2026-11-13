package pfcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	business_metrics "github.com/free5gc/smf/internal/metrics/business"
	"github.com/free5gc/util/metrics/utils"
)

func TestPfcpMessageTypeLabel(t *testing.T) {
	testCases := []struct {
		request message.Message
		want    string
	}{
		{message.NewHeartbeatRequest(0, nil, nil), business_metrics.PFCP_MSG_HEARTBEAT},
		{message.NewSessionEstablishmentRequest(0, 0, 0, 0, 0), business_metrics.PFCP_MSG_SESSION_ESTABLISHMENT},
		{message.NewSessionModificationRequest(0, 0, 0, 0, 0), business_metrics.PFCP_MSG_SESSION_MODIFICATION},
		{message.NewSessionDeletionRequest(0, 0, 0, 0, 0), business_metrics.PFCP_MSG_SESSION_DELETION},
		{message.NewAssociationSetupRequest(0), business_metrics.PFCP_MSG_ASSOCIATION_SETUP},
		{message.NewAssociationReleaseRequest(0, nil), business_metrics.PFCP_MSG_ASSOCIATION_RELEASE},
		{message.NewNodeReportRequest(0), business_metrics.PFCP_MSG_OTHER},
		{nil, business_metrics.PFCP_MSG_OTHER},
	}
	for _, tc := range testCases {
		if got := pfcpMessageTypeLabel(tc.request); got != tc.want {
			t.Errorf("pfcpMessageTypeLabel(%T) = %q, want %q", tc.request, got, tc.want)
		}
	}
}

func TestPfcpResultLabel(t *testing.T) {
	establishment := message.NewSessionEstablishmentRequest(0, 0, 0, 0, 0)
	heartbeat := message.NewHeartbeatRequest(0, nil, nil)

	testCases := []struct {
		name     string
		request  message.Message
		response message.Message
		err      error
		want     string
	}{
		{
			name:    "timeout keeps its meaning through error wrapping",
			request: establishment,
			err:     fmt.Errorf("PFCP Session Establishment Request: %w", errRequestTimedOut),
			want:    business_metrics.PFCP_RESULT_TIMEOUT,
		},
		{
			name:    "canceled association context",
			request: establishment,
			err:     fmt.Errorf("send PFCP request: %w", context.Canceled),
			want:    business_metrics.PFCP_RESULT_ERROR,
		},
		{
			name:     "accepted",
			request:  establishment,
			response: message.NewSessionEstablishmentResponse(0, 0, 1, 0, 0, ie.NewCause(ie.CauseRequestAccepted)),
			want:     business_metrics.PFCP_RESULT_SUCCESS,
		},
		{
			name:     "rejected by the UPF",
			request:  establishment,
			response: message.NewSessionEstablishmentResponse(0, 0, 1, 0, 0, ie.NewCause(ie.CauseNoResourcesAvailable)),
			want:     business_metrics.PFCP_RESULT_REJECTED,
		},
		{
			name:     "response without the mandatory Cause",
			request:  establishment,
			response: message.NewSessionEstablishmentResponse(0, 0, 1, 0, 0),
			want:     business_metrics.PFCP_RESULT_ERROR,
		},
		{
			name:     "response of another type",
			request:  establishment,
			response: message.NewSessionDeletionResponse(0, 0, 1, 0, 0, ie.NewCause(ie.CauseRequestAccepted)),
			want:     business_metrics.PFCP_RESULT_ERROR,
		},
		{
			name:     "heartbeat response has no Cause",
			request:  heartbeat,
			response: message.NewHeartbeatResponse(0, ie.NewRecoveryTimeStamp(time.Now())),
			want:     business_metrics.PFCP_RESULT_SUCCESS,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pfcpResultLabel(tc.request, tc.response, tc.err); got != tc.want {
				t.Errorf("pfcpResultLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSendRequestRecordsPfcpMetrics(t *testing.T) {
	business_metrics.GetPfcpHandlerMetrics("free5gc")
	business_metrics.EnablePfcpMetrics()
	utils.EnableBusinessMetrics()

	s, _ := startTestPfcpServer(t)
	peerAddr, peerDone := startHeartbeatResponsePeer(t, func(req *message.HeartbeatRequest) (message.Message, error) {
		return message.NewHeartbeatResponse(req.Sequence(), ie.NewRecoveryTimeStamp(time.Now())), nil
	})
	blackHole := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}

	if _, err := s.SendHeartbeatRequest(context.Background(), peerAddr); err != nil {
		t.Fatalf("SendHeartbeatRequest() error: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("UPF peer error: %v", err)
	}
	if _, err := s.SendHeartbeatRequest(context.Background(), blackHole); !errors.Is(err, errRequestTimedOut) {
		t.Fatalf("SendHeartbeatRequest() error = %v, want timeout", err)
	}

	for _, result := range []string{business_metrics.PFCP_RESULT_SUCCESS, business_metrics.PFCP_RESULT_TIMEOUT} {
		counter := business_metrics.PfcpRequestCounter.WithLabelValues(
			business_metrics.PFCP_MSG_HEARTBEAT, "127.0.0.1", result)
		if got := testutil.ToFloat64(counter); got != 1 {
			t.Errorf("pfcp_request_total{result=%q} = %v, want 1", result, got)
		}
	}
	// Only the answered request has a latency sample.
	if got := testutil.CollectAndCount(business_metrics.PfcpRequestDurationHist); got != 1 {
		t.Errorf("pfcp_request_duration_seconds series = %d, want 1", got)
	}
}
