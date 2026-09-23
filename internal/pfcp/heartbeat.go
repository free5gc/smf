package pfcp

import (
	"context"
	"fmt"
	"net"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

func (s *PfcpServer) handleHeartbeatRequest(req *message.HeartbeatRequest) *message.HeartbeatResponse {
	return message.NewHeartbeatResponse(
		req.Sequence(),
		ie.NewRecoveryTimeStamp(s.RecoveryTime()),
	)
}

// SendHeartbeatRequest sends an SMF-initiated Heartbeat Request and waits for
// the response matched by the PFCP transaction layer. UPF association state is
// intentionally updated by the caller, not by this transport-level procedure.
func (s *PfcpServer) SendHeartbeatRequest(
	ctx context.Context,
	addr *net.UDPAddr,
) (*message.HeartbeatResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("send PFCP Heartbeat Request: nil PFCP server")
	}
	if addr == nil || addr.IP == nil || addr.IP.IsUnspecified() {
		return nil, fmt.Errorf("send PFCP Heartbeat Request: no destination IP address")
	}

	request := message.NewHeartbeatRequest(
		0, // TxTransaction assigns the final 24-bit sequence number.
		ie.NewRecoveryTimeStamp(s.RecoveryTime()),
		nil,
	)
	received, err := s.sendRequest(ctx, request, addr)
	if err != nil {
		return nil, fmt.Errorf("PFCP Heartbeat Request to %v: %w", addr, err)
	}

	response, ok := received.(*message.HeartbeatResponse)
	if !ok {
		return nil, fmt.Errorf("received unexpected response %T for PFCP Heartbeat Request", received)
	}
	if response.RecoveryTimeStamp == nil {
		return nil, fmt.Errorf("PFCP Heartbeat Response from %v is missing Recovery Time Stamp", addr)
	}
	if _, err = response.RecoveryTimeStamp.RecoveryTimeStamp(); err != nil {
		return nil, fmt.Errorf("decode PFCP Heartbeat Response Recovery Time Stamp from %v: %w", addr, err)
	}
	return response, nil
}
