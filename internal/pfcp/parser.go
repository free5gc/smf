package pfcp

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

const (
	pfcpParseWorkers  = 4
	pfcpParseQueueLen = 64
	// This is a defensive limit, with headroom for normal grouped PFCP IEs.
	// Bounding depth also bounds go-pfcp's repeated MarshalLen subtree walks.
	maxGroupedIEDepth = 16
)

type rawPfcpPacket struct {
	packet []byte
	addr   net.UDPAddr
}

func (s *PfcpServer) parser(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		select {
		case <-s.stopCh:
			return
		case raw := <-s.parseCh:
			if s.rejectUnsupportedPfcpVersion(raw) {
				continue
			}
			packet, ignored, prepareErr := preparePfcpPacket(raw.packet)
			if prepareErr != nil {
				s.log.Debugf("invalid PFCP packet from %v: %v", raw.addr, prepareErr)
				s.queueParseError(raw, prepareErr)
				continue
			}
			if ignored != 0 {
				s.log.Warnf("ignored %d unexpected grouped PFCP IEs from %v", ignored, raw.addr)
			}
			if s.log.Logger.IsLevelEnabled(logrus.TraceLevel) {
				s.log.Tracef("received PFCP message (len=%d):\n%s", len(packet), hex.Dump(packet))
			}
			msg, err := message.Parse(packet)
			if err != nil {
				s.log.Debugf("parse PFCP message from %v: %v", raw.addr, err)
				continue
			}
			if _, unknown := msg.(*message.Generic); unknown {
				continue
			}
			if request, ok := msg.(*message.HeartbeatRequest); ok {
				// Heartbeat has no Cause-bearing error response. Do not dispatch a
				// request that lacks its mandatory timestamp (7.4.2.1, 7.6.6).
				if request.RecoveryTimeStamp == nil {
					continue
				}
				if _, err = request.RecoveryTimeStamp.RecoveryTimeStamp(); err != nil {
					continue
				}
			}
			sendToRcvCh(s.stopCh, s.rcvCh, RcvPfcpMsg{RemoteAddr: raw.addr, Msg: msg}, s.log)
		}
	}
}

// packetParseError preserves enough context to reject a request without
// executing its procedure, or to finish an erroneous response's transaction.
type packetParseError struct {
	cause          uint8
	offending      ie.IEType
	offendingKnown bool
	detail         string
}

func (e *packetParseError) Error() string { return e.detail }

// preparePfcpPacket compacts admitted grouped IEs into a new wire buffer.
// Each admitted IE is visited once; an unexpected group is skipped in O(1).
// Both ChildIEs and Payload seen by subsequent go-pfcp helpers are sanitized,
// so helpers that reparse Payload cannot reintroduce the original subtree.
func preparePfcpPacket(packet []byte) ([]byte, int, error) {
	headerLen := 8
	if len(packet) < headerLen {
		return nil, 0, fmt.Errorf("truncated PFCP header")
	}
	if packet[0]&1 != 0 {
		headerLen += 8
	}
	if len(packet) < headerLen {
		return nil, 0, fmt.Errorf("truncated PFCP session header")
	}
	if err := validatePfcpPacketLength(packet); err != nil {
		return nil, 0, &packetParseError{cause: ie.CauseInvalidLength, detail: err.Error()}
	}
	output := make([]byte, headerLen, len(packet))
	copy(output, packet[:headerLen])
	ignored := 0
	output, err := appendPfcpIEs(output, packet[headerLen:], packet[1], 0, 0, &ignored)
	if err != nil {
		return nil, 0, err
	}
	binary.BigEndian.PutUint16(output[2:4], uint16(len(output)-4))
	return output, ignored, nil
}

func appendPfcpIEs(output, input []byte, messageType uint8, parent ie.IEType, depth int, ignored *int) ([]byte, error) {
	var timestampSeen, sourceSeen bool
	heartbeat := messageType == message.MsgTypeHeartbeatRequest || messageType == message.MsgTypeHeartbeatResponse
	for len(input) != 0 {
		if len(input) < 4 {
			offending, known := parent, parent != 0
			if len(input) >= 2 {
				offending, known = ie.IEType(binary.BigEndian.Uint16(input[:2])), true
			}
			return nil, &packetParseError{
				cause: ie.CauseInvalidLength, offending: offending,
				offendingKnown: known, detail: "truncated PFCP IE header",
			}
		}
		element := ie.IE{Type: ie.IEType(binary.BigEndian.Uint16(input[:2]))}
		length := 4 + int(binary.BigEndian.Uint16(input[2:4]))
		if length > len(input) {
			return nil, &packetParseError{
				cause: ie.CauseInvalidLength, offending: element.Type, offendingKnown: true,
				detail: fmt.Sprintf("PFCP IE %d exceeds enclosing length", element.Type),
			}
		}
		if element.IsVendorSpecific() && length < 6 {
			return nil, &packetParseError{
				cause: ie.CauseInvalidLength, offending: element.Type, offendingKnown: true,
				detail: "PFCP vendor IE is missing enterprise ID",
			}
		}
		if parent == 0 && heartbeat {
			duplicate := false
			switch element.Type {
			case ie.RecoveryTimeStamp:
				duplicate, timestampSeen = timestampSeen, true
			case ie.SourceIPAddress:
				duplicate, sourceSeen = sourceSeen, true
			}
			if duplicate {
				input = input[length:]
				continue
			}
		}
		if !element.IsGrouped() {
			output = append(output, input[:length]...)
		} else if groupedIEAllowed(messageType, parent, element.Type) {
			if depth == maxGroupedIEDepth {
				return nil, &packetParseError{
					cause: ie.CauseRequestRejected, offending: element.Type, offendingKnown: true,
					detail: "PFCP grouped IE resource limit exceeded",
				}
			}
			start := len(output)
			output = append(output, input[:4]...)
			var err error
			output, err = appendPfcpIEs(output, input[4:length], messageType, element.Type, depth+1, ignored)
			if err != nil {
				return nil, err
			}
			binary.BigEndian.PutUint16(output[start+2:start+4], uint16(len(output)-start-4))
		} else {
			*ignored++
		}
		input = input[length:]
	}
	return output, nil
}

func (s *PfcpServer) queueParseError(raw rawPfcpPacket, err error) {
	var parseErr *packetParseError
	if !errors.As(err, &parseErr) {
		return
	} // Too short to identify a transaction safely.
	if len(raw.packet) < 8 {
		return
	}
	headerLen := 8
	if raw.packet[0]&1 != 0 {
		headerLen += 8
	}
	if len(raw.packet) < headerLen {
		return
	}
	header := append([]byte(nil), raw.packet[:headerLen]...)
	binary.BigEndian.PutUint16(header[2:4], uint16(headerLen-4))
	msg, decodeErr := message.Parse(header)
	if decodeErr != nil {
		return
	}
	if _, unknown := msg.(*message.Generic); unknown {
		return
	}
	if msg.MessageType() == message.MsgTypeHeartbeatRequest {
		return
	}
	sendToRcvCh(s.stopCh, s.rcvCh, RcvPfcpMsg{RemoteAddr: raw.addr, Msg: msg, ParseError: parseErr}, s.log)
}

// Version handling precedes v1 IE interpretation (TS 29.244 7.6.2). The
// Version Not Supported response has no regular request/response transaction.
func (s *PfcpServer) rejectUnsupportedPfcpVersion(raw rawPfcpPacket) bool {
	if len(raw.packet) < 8 || raw.packet[0]>>5 == 1 {
		return false
	}
	response, err := message.NewVersionNotSupportedResponse(0).Marshal()
	if err != nil {
		s.log.Debugf("marshal PFCP version response: %v", err)
		return true
	}
	if _, err = s.conn.WriteToUDP(response, &raw.addr); err != nil {
		s.log.Debugf("send PFCP version response: %v", err)
	}
	return true
}
