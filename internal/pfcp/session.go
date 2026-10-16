package pfcp

import (
	"context"
	"fmt"
	"net"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// SendSessionEstablishmentRequest sends one already-built concrete go-pfcp
// request through the server-owned transaction layer. A rejected response is a
// valid protocol result and is returned to the processor; malformed responses
// are transport/protocol errors.
func (s *PfcpServer) SendSessionEstablishmentRequest(
	ctx context.Context,
	request *message.SessionEstablishmentRequest,
	addr *net.UDPAddr,
	localSEID uint64,
) (*message.SessionEstablishmentResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("send PFCP Session Establishment Request: nil PFCP server")
	}
	if request == nil {
		return nil, fmt.Errorf("send PFCP Session Establishment Request: nil request")
	}
	if addr == nil || addr.IP == nil || addr.IP.IsUnspecified() {
		return nil, fmt.Errorf("send PFCP Session Establishment Request: no destination IP address")
	}
	if localSEID == 0 {
		return nil, fmt.Errorf("send PFCP Session Establishment Request: local SEID is zero")
	}

	received, err := s.sendRequest(ctx, request, addr)
	if err != nil {
		return nil, fmt.Errorf("PFCP Session Establishment Request to %v: %w", addr, err)
	}
	response, ok := received.(*message.SessionEstablishmentResponse)
	if !ok {
		return nil, fmt.Errorf(
			"received unexpected response %T for PFCP Session Establishment Request", received,
		)
	}
	if response.SEID() != localSEID {
		return nil, fmt.Errorf(
			"PFCP Session Establishment Response from %v has SEID %d, want %d",
			addr, response.SEID(), localSEID,
		)
	}
	if response.NodeID == nil || response.Cause == nil {
		return nil, fmt.Errorf(
			"PFCP Session Establishment Response from %v is missing mandatory IE(s)", addr,
		)
	}
	if _, err = nodeIDFromIE(response.NodeID); err != nil {
		return nil, fmt.Errorf(
			"decode PFCP Session Establishment Response Node ID from %v: %w", addr, err,
		)
	}
	cause, err := response.Cause.Cause()
	if err != nil {
		return nil, fmt.Errorf(
			"decode PFCP Session Establishment Response Cause from %v: %w", addr, err,
		)
	}
	if cause == ie.CauseRequestAccepted {
		if response.UPFSEID == nil {
			return nil, fmt.Errorf(
				"accepted PFCP Session Establishment Response from %v is missing UP F-SEID", addr,
			)
		}
		if _, err = response.UPFSEID.FSEID(); err != nil {
			return nil, fmt.Errorf(
				"decode PFCP Session Establishment Response UP F-SEID from %v: %w", addr, err,
			)
		}
	}
	return response, nil
}

// SendSessionModificationRequest sends one concrete go-pfcp Session Modification
// Request through the server-owned transaction layer. A rejected response is
// returned to the processor as a valid protocol result.
func (s *PfcpServer) SendSessionModificationRequest(
	ctx context.Context,
	request *message.SessionModificationRequest,
	addr *net.UDPAddr,
	localSEID uint64,
) (*message.SessionModificationResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("send PFCP Session Modification Request: nil PFCP server")
	}
	if request == nil {
		return nil, fmt.Errorf("send PFCP Session Modification Request: nil request")
	}
	if request.SEID() == 0 {
		return nil, fmt.Errorf("send PFCP Session Modification Request: remote SEID is zero")
	}
	if addr == nil || addr.IP == nil || addr.IP.IsUnspecified() {
		return nil, fmt.Errorf("send PFCP Session Modification Request: no destination IP address")
	}
	if localSEID == 0 {
		return nil, fmt.Errorf("send PFCP Session Modification Request: local SEID is zero")
	}

	received, err := s.sendRequest(ctx, request, addr)
	if err != nil {
		return nil, fmt.Errorf("PFCP Session Modification Request to %v: %w", addr, err)
	}
	response, ok := received.(*message.SessionModificationResponse)
	if !ok {
		return nil, fmt.Errorf(
			"received unexpected response %T for PFCP Session Modification Request", received,
		)
	}
	if response.SEID() != localSEID {
		return nil, fmt.Errorf(
			"PFCP Session Modification Response from %v has SEID %d, want %d",
			addr, response.SEID(), localSEID,
		)
	}
	if response.Cause == nil {
		return nil, fmt.Errorf(
			"PFCP Session Modification Response from %v is missing Cause", addr,
		)
	}
	if _, err = response.Cause.Cause(); err != nil {
		return nil, fmt.Errorf(
			"decode PFCP Session Modification Response Cause from %v: %w", addr, err,
		)
	}
	return response, nil
}

// SendSessionDeletionRequest sends one concrete go-pfcp Session Deletion
// Request through the server-owned transaction layer. The request targets the
// UPF SEID, while the response must identify the matching CP-local SEID.
func (s *PfcpServer) SendSessionDeletionRequest(
	ctx context.Context,
	request *message.SessionDeletionRequest,
	addr *net.UDPAddr,
	localSEID uint64,
) (*message.SessionDeletionResponse, error) {
	if s == nil {
		return nil, fmt.Errorf("send PFCP Session Deletion Request: nil PFCP server")
	}
	if request == nil {
		return nil, fmt.Errorf("send PFCP Session Deletion Request: nil request")
	}
	if request.SEID() == 0 {
		return nil, fmt.Errorf("send PFCP Session Deletion Request: remote SEID is zero")
	}
	if addr == nil || addr.IP == nil || addr.IP.IsUnspecified() {
		return nil, fmt.Errorf("send PFCP Session Deletion Request: no destination IP address")
	}
	if localSEID == 0 {
		return nil, fmt.Errorf("send PFCP Session Deletion Request: local SEID is zero")
	}

	received, err := s.sendRequest(ctx, request, addr)
	if err != nil {
		return nil, fmt.Errorf("PFCP Session Deletion Request to %v: %w", addr, err)
	}
	response, ok := received.(*message.SessionDeletionResponse)
	if !ok {
		return nil, fmt.Errorf(
			"received unexpected response %T for PFCP Session Deletion Request", received,
		)
	}
	if response.SEID() != localSEID {
		return nil, fmt.Errorf(
			"PFCP Session Deletion Response from %v has SEID %d, want %d",
			addr, response.SEID(), localSEID,
		)
	}
	if response.Cause == nil {
		return nil, fmt.Errorf(
			"PFCP Session Deletion Response from %v is missing Cause", addr,
		)
	}
	if _, err = response.Cause.Cause(); err != nil {
		return nil, fmt.Errorf(
			"decode PFCP Session Deletion Response Cause from %v: %w", addr, err,
		)
	}
	return response, nil
}
