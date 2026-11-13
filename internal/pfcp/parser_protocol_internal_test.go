package pfcp

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// Expectations come from TS 29.244 V16.7.0/V17.10.0, not from base outputs.
func specTLV(typ ie.IEType, payload []byte) []byte {
	data := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint16(data[:2], uint16(typ))
	binary.BigEndian.PutUint16(data[2:4], uint16(len(payload)))
	copy(data[4:], payload)
	return data
}

func specNestedGroup(depth int) []byte {
	data := specTLV(ie.PDRID, []byte{0, 1})
	for i := 0; i < depth; i++ {
		data = specTLV(ie.CreatePDR, data)
	}
	return data
}

func specAppend(packet, extra []byte) []byte {
	result := append(append([]byte(nil), packet...), extra...)
	binary.BigEndian.PutUint16(result[2:4], uint16(len(result)-4))
	return result
}

func specPeer(t *testing.T, server *PfcpServer) *net.UDPConn {
	t.Helper()
	peer, err := net.DialUDP("udp", nil, server.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return peer
}

func specExchange(peer *net.UDPConn, packet []byte) (message.Message, error) {
	if _, err := peer.Write(packet); err != nil {
		return nil, err
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return nil, err
	}
	buffer := make([]byte, 2048)
	n, err := peer.Read(buffer)
	if err != nil {
		return nil, err
	}
	return message.Parse(buffer[:n])
}

func specAssertHeartbeat(t *testing.T, server *PfcpServer, peer *net.UDPConn, sequence uint32, extra []byte) {
	t.Helper()
	packet, err := message.NewHeartbeatRequest(sequence, ie.NewRecoveryTimeStamp(time.Now()), nil).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := specExchange(peer, specAppend(packet, extra))
	if err != nil {
		t.Errorf("TS29.244 7.6.9 requires ignoring the unexpected IE and processing Heartbeat: %v", err)
		return
	}
	response, ok := decoded.(*message.HeartbeatResponse)
	if !ok {
		t.Errorf("response=%T, want HeartbeatResponse", decoded)
		return
	}
	if response.Sequence() != sequence {
		t.Errorf("sequence=%d, want %d", response.Sequence(), sequence)
	}
	if response.RecoveryTimeStamp == nil {
		t.Error("mandatory RecoveryTimeStamp missing")
		return
	}
	stamp, err := response.RecoveryTimeStamp.RecoveryTimeStamp()
	if err != nil {
		t.Error(err)
		return
	}
	if stamp.Unix() != server.RecoveryTime().Unix() {
		t.Errorf("RecoveryTimeStamp=%s, want server start %s", stamp, server.RecoveryTime())
	}
}

func Test1197SpecHeartbeatExtensions(t *testing.T) {
	cases := []struct {
		name  string
		extra []byte
	}{
		{"ordinary", nil},
		{"unknown_opaque", specTLV(0x7fff, []byte{0xff})},
		{"vendor_opaque", specTLV(0x8001, []byte{0, 1, 0xff})},
		{"unexpected_group_depth16", specNestedGroup(16)},
		{"unexpected_group_depth17", specNestedGroup(17)},
	}
	for i, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, _ := startTestPfcpServer(t)
			peer := specPeer(t, server)
			specAssertHeartbeat(t, server, peer, uint32(300+i), test.extra)
			// A second ordinary request proves socket/dispatcher liveness even if the
			// first request was incorrectly discarded. No deployed service is used.
			specAssertHeartbeat(t, server, peer, uint32(400+i), nil)
		})
	}
}

func Test1197SpecAssociationUpdateNormal(t *testing.T) {
	server, _ := startTestPfcpServer(t)
	state := &fakeAssociationState{updateCause: 1}
	server.SetAssociationStateManager(state)
	peer := specPeer(t, server)
	packet, err := message.NewAssociationUpdateRequest(501, ie.NewNodeIDHeuristic("127.0.0.1")).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := specExchange(peer, packet)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := decoded.(*message.AssociationUpdateResponse)
	if !ok {
		t.Fatalf("response=%T", decoded)
	}
	specAssertAssociationResponse(t, response, 501, 1)
	state.mu.Lock()
	called := state.updateCalled
	remote := state.remote
	state.mu.Unlock()
	if !called {
		t.Error("valid update did not reach association state manager")
	}
	if remote == nil || !remote.IP.IsLoopback() {
		t.Errorf("source address lost: %v", remote)
	}
}

func specAssertAssociationResponse(
	t *testing.T, response *message.AssociationUpdateResponse, sequence uint32, cause uint8,
) {
	t.Helper()
	if response.Sequence() != sequence {
		t.Errorf("sequence=%d, want %d", response.Sequence(), sequence)
	}
	if response.NodeID == nil {
		t.Error("mandatory response NodeID missing")
	} else {
		value, err := response.NodeID.NodeID()
		if err != nil || value != "127.0.0.1" {
			t.Errorf("NodeID=%q, err=%v", value, err)
		}
	}
	if response.Cause == nil {
		t.Error("mandatory Cause missing")
		return
	}
	value, err := response.Cause.Cause()
	if err != nil || value != cause {
		t.Errorf("Cause=%d, err=%v; want %d", value, err, cause)
	}
}

func Test1197SpecInvalidIELengthResponse(t *testing.T) {
	server, _ := startTestPfcpServer(t)
	state := &fakeAssociationState{updateCause: 1}
	server.SetAssociationStateManager(state)
	peer := specPeer(t, server)
	packet, err := message.NewAssociationUpdateRequest(502, ie.NewNodeIDHeuristic("127.0.0.1")).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// A complete PFCP header and NodeID followed by a final IE that declares
	// one value octet but carries none. The overall PFCP length stays exact.
	invalid := specTLV(ie.PFCPAUReqFlags, nil)
	binary.BigEndian.PutUint16(invalid[2:4], 1)
	decoded, readErr := specExchange(peer, specAppend(packet, invalid))
	t.Run("Cause68", func(t *testing.T) {
		if readErr != nil {
			t.Errorf("TS29.244 7.6.7 requires Invalid length response, got %v", readErr)
			return
		}
		response, ok := decoded.(*message.AssociationUpdateResponse)
		if !ok {
			t.Errorf("response=%T", decoded)
			return
		}
		specAssertAssociationResponse(t, response, 502, 68)
	})
	t.Run("OffendingType", func(t *testing.T) {
		if readErr != nil {
			t.Errorf("no response containing offending IE type: %v", readErr)
			return
		}
		response, ok := decoded.(*message.AssociationUpdateResponse)
		if !ok {
			t.Errorf("response=%T", decoded)
			return
		}
		var offending *ie.IE
		for _, element := range response.IEs {
			if element.Type == ie.OffendingIE {
				offending = element
				break
			}
		}
		if offending == nil {
			t.Error("required Offending IE missing")
			return
		}
		typ, offendingErr := offending.OffendingIE()
		if offendingErr != nil || typ != ie.PFCPAUReqFlags {
			t.Errorf("offending type=%d, err=%v, want %d", typ, offendingErr, ie.PFCPAUReqFlags)
		}
	})
	state.mu.Lock()
	called := state.updateCalled
	state.mu.Unlock()
	if called {
		t.Error("invalid request mutated association state")
	}
	specAssertHeartbeat(t, server, peer, 503, nil)
	t.Logf("wire case: exact PFCP length, last IE type=%d declared=1 actual=0; association callback=%v",
		ie.PFCPAUReqFlags, called)
}
