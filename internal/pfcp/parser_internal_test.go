package pfcp

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

// nestedGroupedPacket builds wire bytes directly: using IE constructors would
// itself recursively marshal the tree before the receiver can be tested.
func nestedGroupedPacket(depth int) []byte {
	packet := make([]byte, 8+4*depth+5)
	packet[0], packet[1], packet[6] = 0x20, message.MsgTypeHeartbeatRequest, 1
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)-4))
	for offset := 8; offset < 8+4*depth; offset += 4 {
		binary.BigEndian.PutUint16(packet[offset:offset+2], uint16(ie.CreatePDR))
		binary.BigEndian.PutUint16(packet[offset+2:offset+4], uint16(len(packet)-offset-4))
	}
	offset := 8 + 4*depth
	binary.BigEndian.PutUint16(packet[offset:offset+2], uint16(ie.Cause))
	binary.BigEndian.PutUint16(packet[offset+2:offset+4], 1)
	return packet
}

func TestReceiverRejectsDeepGroupedIEAndServesHeartbeat(t *testing.T) {
	server, _ := startTestPfcpServer(t)
	delivered := make(chan uint32, 2)
	server.SetDispatch(func(msg message.Message, _ *net.UDPAddr) {
		delivered <- msg.Sequence()
	})
	peer, err := net.DialUDP("udp", nil, server.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, err = peer.Write(nestedGroupedPacket(16000)); err != nil {
		t.Fatal(err)
	}
	heartbeat, err := message.NewHeartbeatRequest(2, ie.NewRecoveryTimeStamp(time.Now()), nil).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err = peer.Write(heartbeat); err != nil {
		t.Fatal(err)
	}
	select {
	case sequence := <-delivered:
		if sequence != 2 {
			t.Fatalf("dispatched sequence %d; deeply nested packet must be rejected", sequence)
		}
		t.Logf("valid heartbeat dispatched after %s", time.Since(started))
	case <-time.After(time.Second):
		t.Fatal("deeply nested packet blocked delivery of the next heartbeat")
	}
}

func packetWithPayload(payload []byte, withSEID bool) []byte {
	headerLength := 8
	flags := byte(0x20)
	if withSEID {
		headerLength += 8
		flags |= 1
	}
	packet := make([]byte, headerLength+len(payload))
	packet[0], packet[1] = flags, message.MsgTypeHeartbeatRequest
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)-4))
	copy(packet[headerLength:], payload)
	return packet
}

func TestPreparePfcpPacket(t *testing.T) {
	tests := []struct {
		name      string
		packet    []byte
		wantError bool
	}{
		{"empty node message", packetWithPayload(nil, false), false},
		{"empty session message", packetWithPayload(nil, true), false},
		{"depth limit", nestedGroupedPacket(16), false},
		{"unexpected group beyond limit is skipped", nestedGroupedPacket(17), false},
		{"issue 1197 unexpected group is skipped", nestedGroupedPacket(16000), false},
		{"missing header", nil, true},
		{"short node header", []byte{0x20, 1, 0, 0}, true},
		{"short session header", []byte{0x21, 1, 0, 4, 0, 0, 0, 0}, true},
		{"trailing octet", packetWithPayload([]byte{0}, false), true},
		{"truncated leaf", packetWithPayload([]byte{0, 19, 0, 2, 0}, false), true},
		{"unexpected group child is opaque", packetWithPayload([]byte{0, 1, 0, 4, 0, 19, 0, 1}, false), false},
		{"unexpected group invalid contents ignored", packetWithPayload([]byte{0, 1, 0, 1, 0}, false), false},
		{"empty group", packetWithPayload([]byte{0, 1, 0, 0}, false), false},
		{"opaque unknown IE", packetWithPayload([]byte{0x7f, 0xff, 0, 1, 0xff}, false), false},
		{"opaque vendor IE", packetWithPayload([]byte{0x80, 1, 0, 3, 0, 1, 0xff}, false), false},
		{"missing enterprise ID", packetWithPayload([]byte{0x80, 1, 0, 1, 0}, false), true},
		{"empty vendor IE", packetWithPayload([]byte{0x80, 1, 0, 2, 0, 1}, true), false},
	}
	// A sibling at the root must not inherit the previous sibling's depth.
	atLimit := nestedGroupedPacket(16)
	siblings := append(append([]byte(nil), atLimit[8:]...), atLimit[8:]...)
	tests = append(tests, struct {
		name      string
		packet    []byte
		wantError bool
	}{
		"sibling groups at depth limit", packetWithPayload(siblings, true), false,
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared, _, err := preparePfcpPacket(test.packet)
			if (err != nil) != test.wantError {
				t.Fatalf("validation error = %v, wantError = %v", err, test.wantError)
			}
			if err == nil {
				if _, parseErr := message.Parse(prepared); parseErr != nil {
					t.Fatalf("accepted packet fails decoder: %v", parseErr)
				}
			}
		})
	}
}

func TestHeartbeatSkipsUnexpectedDecoderGroupedTypes(t *testing.T) {
	// The guard must cover every type the dependency recursively decodes,
	// rather than just Create PDR used by the issue's reproducer.
	for typ := 0; typ <= 0xffff; typ++ {
		element := ie.IE{Type: ie.IEType(typ)}
		if !element.IsGrouped() {
			continue
		}
		packet := nestedGroupedPacket(17)
		binary.BigEndian.PutUint16(packet[8:10], uint16(typ))
		prepared, ignored, err := preparePfcpPacket(packet)
		if err != nil || ignored != 1 || len(prepared) != 8 {
			t.Fatalf("unexpected group %d: length=%d ignored=%d err=%v", typ, len(prepared), ignored, err)
		}
	}
}

type drainingReadConn struct {
	transientReadErrorConn
	packets [][]byte
	drained chan struct{}
	next    int
}

func (c *drainingReadConn) ReadFromUDP(buffer []byte) (int, *net.UDPAddr, error) {
	if c.next == len(c.packets) {
		close(c.drained)
		<-c.closed
		return 0, nil, net.ErrClosed
	}
	packet := c.packets[c.next]
	c.next++
	return copy(buffer, packet), c.peer, nil
}

func TestReceiverDrainsSocketWhenParseQueueIsFull(t *testing.T) {
	server := NewPfcpServer(nil, "127.0.0.1")
	connection := &drainingReadConn{
		transientReadErrorConn: transientReadErrorConn{
			peer:   &net.UDPAddr{IP: net.ParseIP("192.0.2.81"), Port: PfcpPort},
			closed: make(chan struct{}),
		},
		drained: make(chan struct{}),
	}
	for sequence := 1; sequence <= pfcpParseQueueLen+8; sequence++ {
		packet := nestedGroupedPacket(0)
		packet[6] = byte(sequence)
		connection.packets = append(connection.packets, packet)
	}
	server.conn = connection
	var wg sync.WaitGroup
	wg.Add(1)
	go server.receiver(&wg)
	t.Cleanup(func() { server.Stop(); wg.Wait() })
	select {
	case <-connection.drained:
	case <-time.After(time.Second):
		t.Fatal("receiver stopped reading while parse queue was full")
	}
	if got := len(server.parseCh); got != pfcpParseQueueLen {
		t.Fatalf("queued packets = %d, want %d", got, pfcpParseQueueLen)
	}
	// The receiver reuses its read buffer. Queued packets must own their bytes.
	for sequence := 1; sequence <= pfcpParseQueueLen; sequence++ {
		raw := <-server.parseCh
		if raw.packet[6] != byte(sequence) {
			t.Fatalf("packet bytes overwritten: sequence = %d, want %d", raw.packet[6], sequence)
		}
	}
}

func FuzzPfcpStructureGuard(f *testing.F) {
	seed := packetWithPayload(specTLV(ie.CreatedPDR, specTLV(ie.PDRID, []byte{0, 1})), true)
	seed[1] = message.MsgTypeSessionEstablishmentResponse
	f.Add(seed)
	f.Add(nestedGroupedPacket(0))
	f.Add(nestedGroupedPacket(16))
	f.Add(nestedGroupedPacket(17))
	f.Add(packetWithPayload([]byte{0x80, 1, 0, 2, 0, 1}, true))
	f.Fuzz(func(t *testing.T, packet []byte) {
		prepared, _, prepareErr := preparePfcpPacket(packet)
		if prepareErr != nil {
			return
		}
		// Every concrete message uses the same IE decoder. Select Heartbeat to
		// avoid the dependency logging an unknown message type for random inputs.
		prepared[1] = message.MsgTypeHeartbeatRequest
		if _, err := message.Parse(prepared); err != nil {
			t.Fatalf("guard accepted an invalid IE encoding: %v", err)
		}
	})
}
