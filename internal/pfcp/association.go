package pfcp

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

// AssociationStateManager isolates PFCP protocol handling from SMF context.
// The runtime adapter will look up configured UPFs, update association state,
// and return any restart-recovery work as afterResponse.
type AssociationStateManager interface {
	SetupAssociation(peer pfcptype.NodeID, recoveryTime time.Time) (cause uint8, afterResponse func())
	UpdateAssociation(peer pfcptype.NodeID, request *message.AssociationUpdateRequest) (cause uint8, afterResponse func())
	ReleaseAssociation(peer pfcptype.NodeID) (cause uint8)
}

func (s *PfcpServer) SetAssociationStateManager(manager AssociationStateManager) {
	s.associationMu.Lock()
	s.associationState = manager
	s.associationMu.Unlock()
}

func (s *PfcpServer) handleAssociationSetupRequest(
	request *message.AssociationSetupRequest,
) (*message.AssociationSetupResponse, func()) {
	responseIEs := []*ie.IE{
		ie.NewCause(ie.CauseMandatoryIEMissing),
		ie.NewCPFunctionFeatures(0),
		ie.NewRecoveryTimeStamp(s.RecoveryTime()),
	}
	if localNodeID := s.localNodeIDIE(); localNodeID != nil {
		responseIEs = append([]*ie.IE{localNodeID}, responseIEs...)
	}
	response := message.NewAssociationSetupResponse(request.Sequence(), responseIEs...)
	if request.NodeID == nil || request.RecoveryTimeStamp == nil {
		s.log.Warn("PFCP Association Setup Request is missing Node ID or Recovery Time Stamp")
		return response, nil
	}

	peer, err := nodeIDFromIE(request.NodeID)
	if err != nil {
		s.log.Warnf("decode PFCP Association Setup Request Node ID: %v", err)
		response.Cause = ie.NewCause(ie.CauseMandatoryIEIncorrect)
		return response, nil
	}
	recoveryTime, err := request.RecoveryTimeStamp.RecoveryTimeStamp()
	if err != nil {
		s.log.Warnf("decode PFCP Association Setup Request Recovery Time Stamp: %v", err)
		response.Cause = ie.NewCause(ie.CauseMandatoryIEIncorrect)
		return response, nil
	}

	s.associationMu.RLock()
	manager := s.associationState
	s.associationMu.RUnlock()
	if manager == nil {
		s.log.Warnf(
			"reject PFCP Association Setup Request from %s: association state manager is not configured",
			peer.String(),
		)
		response.Cause = ie.NewCause(ie.CauseRequestRejected)
		return response, nil
	}

	cause, afterResponse := manager.SetupAssociation(peer, recoveryTime)
	response.Cause = ie.NewCause(cause)
	if cause != ie.CauseRequestAccepted {
		return response, nil
	}
	return response, afterResponse
}

func (s *PfcpServer) handleAssociationUpdateRequest(
	request *message.AssociationUpdateRequest,
) (*message.AssociationUpdateResponse, func()) {
	responseIEs := []*ie.IE{
		ie.NewCause(ie.CauseMandatoryIEMissing),
		ie.NewCPFunctionFeatures(0),
	}
	if localNodeID := s.localNodeIDIE(); localNodeID != nil {
		responseIEs = append([]*ie.IE{localNodeID}, responseIEs...)
	}
	response := message.NewAssociationUpdateResponse(request.Sequence(), responseIEs...)
	if request.NodeID == nil {
		s.log.Warn("PFCP Association Update Request is missing Node ID")
		return response, nil
	}

	peer, err := nodeIDFromIE(request.NodeID)
	if err != nil {
		s.log.Warnf("decode PFCP Association Update Request Node ID: %v", err)
		response.Cause = ie.NewCause(ie.CauseMandatoryIEIncorrect)
		return response, nil
	}
	cause, err := validateAssociationUpdate(request)
	if err != nil {
		s.log.Warnf("decode PFCP Association Update Request from %s: %v", peer.String(), err)
		response.Cause = ie.NewCause(cause)
		return response, nil
	}

	s.associationMu.RLock()
	manager := s.associationState
	s.associationMu.RUnlock()
	if manager == nil {
		s.log.Warnf(
			"reject PFCP Association Update Request from %s: association state manager is not configured",
			peer.String(),
		)
		response.Cause = ie.NewCause(ie.CauseNoEstablishedPFCPAssociation)
		return response, nil
	}

	cause, afterResponse := manager.UpdateAssociation(peer, request)
	response.Cause = ie.NewCause(cause)
	if cause != ie.CauseRequestAccepted {
		return response, nil
	}
	return response, afterResponse
}

func validateAssociationUpdate(request *message.AssociationUpdateRequest) (uint8, error) {
	if request == nil {
		return ie.CauseRequestRejected, fmt.Errorf("nil PFCP Association Update Request")
	}
	if request.UPFunctionFeatures != nil {
		features, err := request.UPFunctionFeatures.UPFunctionFeatures()
		if err != nil || len(features) < 2 {
			if err == nil {
				err = fmt.Errorf("UP Function Features payload is too short: %d", len(features))
			}
			return ie.CauseInvalidLength, err
		}
	}

	var releaseRequested, usageReportsSent bool
	if request.PFCPAssociationReleaseRequest != nil {
		if _, err := request.PFCPAssociationReleaseRequest.PFCPAssociationReleaseRequest(); err != nil {
			return ie.CauseInvalidLength, fmt.Errorf("PFCP Association Release Request IE: %w", err)
		}
		releaseRequested = request.PFCPAssociationReleaseRequest.HasSARR()
		usageReportsSent = request.PFCPAssociationReleaseRequest.HasURSS()
	}
	if request.GracefulReleasePeriod != nil {
		if request.PFCPAssociationReleaseRequest == nil {
			return ie.CauseConditionalIEMissing,
				fmt.Errorf("graceful release period is present without PFCP Association Release Request IE")
		}
		if _, err := request.GracefulReleasePeriod.GracefulReleasePeriod(); err != nil {
			return ie.CauseInvalidLength, fmt.Errorf("graceful release period IE: %w", err)
		}
		if !releaseRequested && !usageReportsSent {
			return ie.CauseRequestRejected,
				fmt.Errorf("graceful release period is present but neither SARR nor URSS is set")
		}
	}
	if request.PFCPAUReqFlags != nil {
		if _, err := request.PFCPAUReqFlags.PFCPAUReqFlags(); err != nil {
			return ie.CauseInvalidLength, fmt.Errorf("PFCPAUReq-Flags IE: %w", err)
		}
	}
	return ie.CauseRequestAccepted, nil
}

func (s *PfcpServer) handleAssociationReleaseRequest(
	request *message.AssociationReleaseRequest,
) *message.AssociationReleaseResponse {
	response := message.NewAssociationReleaseResponse(
		request.Sequence(),
		s.localNodeIDIE(),
		ie.NewCause(ie.CauseMandatoryIEMissing),
	)
	if request.NodeID == nil {
		s.log.Warn("PFCP Association Release Request is missing Node ID")
		return response
	}

	peer, err := nodeIDFromIE(request.NodeID)
	if err != nil {
		s.log.Warnf("decode PFCP Association Release Request Node ID: %v", err)
		response.Cause = ie.NewCause(ie.CauseMandatoryIEIncorrect)
		return response
	}

	s.associationMu.RLock()
	manager := s.associationState
	s.associationMu.RUnlock()
	if manager == nil {
		response.Cause = ie.NewCause(ie.CauseNoEstablishedPFCPAssociation)
		return response
	}
	response.Cause = ie.NewCause(manager.ReleaseAssociation(peer))
	return response
}

// SendAssociationSetupRequest establishes an SMF-initiated PFCP association.
// It validates protocol-level mandatory IEs and returns only an accepted
// concrete response. The caller owns the UPF association state transition.
func (s *PfcpServer) SendAssociationSetupRequest(
	ctx context.Context,
	addr *net.UDPAddr,
) (*message.AssociationSetupResponse, error) {
	if err := validateAssociationDestination(s, addr); err != nil {
		return nil, err
	}
	request := message.NewAssociationSetupRequest(
		0, // TxTransaction assigns the final 24-bit sequence number.
		s.localNodeIDIE(),
		ie.NewRecoveryTimeStamp(s.RecoveryTime()),
		ie.NewCPFunctionFeatures(0),
	)
	received, err := s.sendRequest(ctx, request, addr)
	if err != nil {
		return nil, fmt.Errorf("PFCP Association Setup Request to %v: %w", addr, err)
	}
	response, ok := received.(*message.AssociationSetupResponse)
	if !ok {
		return nil, fmt.Errorf("received unexpected response %T for PFCP Association Setup Request", received)
	}
	if response.NodeID == nil || response.Cause == nil || response.RecoveryTimeStamp == nil {
		return nil, fmt.Errorf("PFCP Association Setup Response from %v is missing mandatory IE(s)", addr)
	}
	if _, err = nodeIDFromIE(response.NodeID); err != nil {
		return nil, fmt.Errorf("decode PFCP Association Setup Response Node ID from %v: %w", addr, err)
	}
	if _, err = response.RecoveryTimeStamp.RecoveryTimeStamp(); err != nil {
		return nil, fmt.Errorf("decode PFCP Association Setup Response Recovery Time Stamp from %v: %w", addr, err)
	}
	if err = requireAcceptedCause(response.Cause); err != nil {
		return nil, fmt.Errorf("PFCP Association Setup Response from %v: %w", addr, err)
	}
	return response, nil
}

func (s *PfcpServer) SendAssociationReleaseRequest(
	ctx context.Context,
	addr *net.UDPAddr,
) (*message.AssociationReleaseResponse, error) {
	if err := validateAssociationDestination(s, addr); err != nil {
		return nil, err
	}
	request := message.NewAssociationReleaseRequest(0, s.localNodeIDIE())
	received, err := s.sendRequest(ctx, request, addr)
	if err != nil {
		return nil, fmt.Errorf("PFCP Association Release Request to %v: %w", addr, err)
	}
	response, ok := received.(*message.AssociationReleaseResponse)
	if !ok {
		return nil, fmt.Errorf("received unexpected response %T for PFCP Association Release Request", received)
	}
	if response.Cause == nil {
		return nil, fmt.Errorf("PFCP Association Release Response from %v is missing mandatory Cause IE", addr)
	}
	if err = requireAcceptedCause(response.Cause); err != nil {
		return nil, fmt.Errorf("PFCP Association Release Response from %v: %w", addr, err)
	}
	return response, nil
}

func validateAssociationDestination(s *PfcpServer, addr *net.UDPAddr) error {
	if s == nil {
		return fmt.Errorf("send PFCP Association Request: nil PFCP server")
	}
	if addr == nil || addr.IP == nil || addr.IP.IsUnspecified() {
		return fmt.Errorf("send PFCP Association Request: no destination IP address")
	}
	if s.localNodeIDIE() == nil {
		return fmt.Errorf("send PFCP Association Request: SMF Node ID is not configured")
	}
	return nil
}

func (s *PfcpServer) localNodeIDIE() *ie.IE {
	nodeID := ""
	if s != nil && s.smfIface != nil && s.Config() != nil &&
		s.Config().Configuration != nil && s.Config().Configuration.PFCP != nil {
		nodeID = s.Config().Configuration.PFCP.NodeID
	}
	if nodeID == "" && s != nil {
		nodeID = s.addr
	}
	if ip := net.ParseIP(nodeID); ip != nil && ip.IsUnspecified() {
		return nil
	}
	return ie.NewNodeIDHeuristic(nodeID)
}

func nodeIDFromIE(nodeIDIE *ie.IE) (pfcptype.NodeID, error) {
	if nodeIDIE == nil {
		return pfcptype.NodeID{}, fmt.Errorf("node ID IE is missing")
	}
	value, err := nodeIDIE.NodeID()
	if err != nil {
		return pfcptype.NodeID{}, err
	}
	if len(nodeIDIE.Payload) == 0 {
		return pfcptype.NodeID{}, fmt.Errorf("node ID payload is empty")
	}
	switch nodeIDIE.Payload[0] {
	case ie.NodeIDIPv4Address:
		ip := net.ParseIP(value).To4()
		if ip == nil {
			return pfcptype.NodeID{}, fmt.Errorf("invalid IPv4 Node ID %q", value)
		}
		return pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: ip}, nil
	case ie.NodeIDIPv6Address:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return pfcptype.NodeID{}, fmt.Errorf("invalid IPv6 Node ID %q", value)
		}
		return pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv6Address, IP: ip.To16()}, nil
	case ie.NodeIDFQDN:
		if value == "" {
			return pfcptype.NodeID{}, fmt.Errorf("empty FQDN Node ID")
		}
		return pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeFqdn, FQDN: value}, nil
	default:
		return pfcptype.NodeID{}, fmt.Errorf("unsupported Node ID type %d", nodeIDIE.Payload[0])
	}
}

func requireAcceptedCause(causeIE *ie.IE) error {
	if causeIE == nil {
		return fmt.Errorf("missing mandatory Cause IE")
	}
	cause, err := causeIE.Cause()
	if err != nil {
		return fmt.Errorf("decode Cause IE: %w", err)
	}
	if cause != ie.CauseRequestAccepted {
		return fmt.Errorf("request rejected with Cause %d", cause)
	}
	return nil
}
