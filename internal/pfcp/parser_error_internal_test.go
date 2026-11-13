package pfcp

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

func startProtocolTestServer(t *testing.T) *PfcpServer {
	t.Helper()
	server := NewPfcpServer(nil, "127.0.0.1")
	server.port, server.dispatchWorkers = 0, 2
	server.retransTimeout = time.Second
	server.maxRetrans = 1
	var wg sync.WaitGroup
	if err := server.Run(&wg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.Stop()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server workers did not stop")
		}
	})
	return server
}

func protocolWire(t *testing.T, typ uint8, session bool, sequence uint32, payload []byte) []byte {
	t.Helper()
	s := uint8(0)
	if session {
		s = 1
	}
	wire, err := message.NewHeader(1, 0, 0, s, typ, 99, sequence, 0, payload).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestParseErrorsUseCorrespondingResponseAndCache(t *testing.T) {
	cases := []struct {
		typ       uint8
		session   bool
		offending ie.IEType
		name      string
	}{
		{message.MsgTypeAssociationSetupRequest, false, ie.NodeID, "association setup"},
		{message.MsgTypeAssociationUpdateRequest, false, ie.PFCPAUReqFlags, "association update"},
		{message.MsgTypeAssociationUpdateRequest, false, 0, "reserved type with invalid length"},
		{message.MsgTypeAssociationReleaseRequest, false, ie.NodeID, "association release"},
		{message.MsgTypeNodeReportRequest, false, ie.NodeReportType, "node report"},
		{message.MsgTypeSessionSetDeletionRequest, false, ie.NodeID, "session set deletion"},
		{message.MsgTypeSessionReportRequest, true, ie.ReportType, "session report"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := startProtocolTestServer(t)
			var calls atomic.Int32
			server.SetDispatch(func(message.Message, *net.UDPAddr) { calls.Add(1) })
			peer := specPeer(t, server)
			invalid := specTLV(test.offending, nil)
			binary.BigEndian.PutUint16(invalid[2:4], 1)
			packet := protocolWire(t, test.typ, test.session, 601, invalid)
			var first []byte
			for attempt := 0; attempt < 2; attempt++ {
				reply, err := specExchange(peer, packet)
				if err != nil {
					t.Fatal(err)
				}
				if reply.MessageType() != test.typ+1 || reply.Sequence() != 601 {
					t.Fatalf("wrong response %T/%d", reply, reply.Sequence())
				}
				if test.session && reply.SEID() != 0 {
					t.Errorf("protocol rejection SEID=%d, want 0", reply.SEID())
				}
				raw := make([]byte, reply.MarshalLen())
				err = reply.MarshalTo(raw)
				if err != nil {
					t.Fatal(err)
				}
				if attempt == 0 {
					first = raw
				} else if !bytes.Equal(first, raw) {
					t.Error("duplicate did not replay the cached rejection")
				}
				header, err := message.ParseHeader(raw)
				if err != nil {
					t.Fatal(err)
				}
				elements, err := ie.ParseMultiIEs(header.Payload)
				if err != nil {
					t.Fatal(err)
				}
				var cause, offending, node *ie.IE
				for _, element := range elements {
					switch element.Type {
					case ie.Cause:
						cause = element
					case ie.OffendingIE:
						offending = element
					case ie.NodeID:
						node = element
					}
				}
				if cause == nil || offending == nil {
					t.Fatal("Cause or Offending IE missing")
				}
				value, causeErr := cause.Cause()
				typ, typeErr := offending.OffendingIE()
				if causeErr != nil || typeErr != nil || value != 68 || typ != test.offending {
					t.Errorf("Cause=%d offending=%d errors=%v/%v", value, typ, causeErr, typeErr)
				}
				if !test.session && node == nil {
					t.Error("mandatory Node ID missing")
				}
			}
			if calls.Load() != 0 {
				t.Error("malformed request reached the procedure callback")
			}
		})
	}
}

func TestWrongPFCPMessageLengthGetsErrorResponse(t *testing.T) {
	server := startProtocolTestServer(t)
	peer := specPeer(t, server)
	wire := protocolWire(t, message.MsgTypeAssociationUpdateRequest, false, 602, nil)
	binary.BigEndian.PutUint16(wire[2:4], 5) // The header is complete; total length is inconsistent.
	decoded, err := specExchange(peer, wire)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := decoded.(*message.AssociationUpdateResponse)
	if !ok {
		t.Fatalf("response=%T", decoded)
	}
	specAssertAssociationResponse(t, response, 602, 68)
}

func TestErroneousResponseCompletesOutstandingTransaction(t *testing.T) {
	server := startProtocolTestServer(t)
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := peer.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	sent := make(chan error, 1)
	go func() {
		if deadlineErr := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); deadlineErr != nil {
			sent <- deadlineErr
			return
		}
		buffer := make([]byte, 2048)
		n, addr, readErr := peer.ReadFromUDP(buffer)
		if readErr != nil {
			sent <- readErr
			return
		}
		_, parseErr := message.Parse(buffer[:n])
		if parseErr != nil {
			sent <- parseErr
			return
		}
		invalid := specTLV(ie.RecoveryTimeStamp, nil)
		binary.BigEndian.PutUint16(invalid[2:4], 4)
		reply := append([]byte(nil), buffer[:8]...)
		reply[1] = message.MsgTypeHeartbeatResponse
		reply = specAppend(reply, invalid)
		_, writeErr := peer.WriteToUDP(reply, addr)
		sent <- writeErr
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = server.SendHeartbeatRequest(ctx, peer.LocalAddr().(*net.UDPAddr))
	if err == nil || !strings.Contains(err.Error(), "exceeds enclosing length") {
		t.Fatalf("expected immediate parse error, got %v", err)
	}
	select {
	case writeErr := <-sent:
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	active := 0
	server.txTrans.Range(func(_, _ any) bool { active++; return true })
	if active != 0 {
		t.Fatalf("erroneous response left %d active transactions", active)
	}
}

func TestExpectedGroupPayloadIsSanitizedForLaterReparse(t *testing.T) {
	// Created PDR -> unexpected Create PDR is not a legal grouped edge. Its
	// payload must be skipped even when the concrete accessor reparses Payload.
	pdrID := specTLV(ie.PDRID, []byte{0, 7})
	children := append(append([]byte(nil), pdrID...), nestedGroupedPacket(16000)[8:]...)
	packet := protocolWire(t, message.MsgTypeSessionEstablishmentResponse, true, 603, specTLV(ie.CreatedPDR, children))
	output, ignored, err := preparePfcpPacket(packet)
	if err != nil || ignored != 1 {
		t.Fatalf("ignored=%d err=%v", ignored, err)
	}
	decoded, err := message.Parse(output)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := decoded.(*message.SessionEstablishmentResponse)
	if !ok || len(response.CreatedPDR) != 1 {
		t.Fatal("Created PDR was lost")
	}
	elements, err := response.CreatedPDR[0].CreatedPDR()
	if err != nil || len(elements) != 1 {
		t.Fatalf("reparse children=%d err=%v", len(elements), err)
	}
	id, err := elements[0].PDRID()
	if err != nil || id != 7 {
		t.Fatalf("PDR ID=%d err=%v", id, err)
	}
	if !bytes.Equal(response.CreatedPDR[0].Payload, pdrID) {
		t.Error("unsafe bytes retained in Payload")
	}
}

func TestLegalGroupedEdgesPreservePayload(t *testing.T) {
	// TS 29.244 7.5.3.7: ATSSS -> MPTCP Parameters; 7.5.8.6:
	// Session Report -> Access Availability Report. These are legal nested groups.
	cases := []struct {
		messageType uint8
		root, child ie.IEType
		leaf        []byte
	}{
		{
			message.MsgTypeSessionEstablishmentResponse, ie.ATSSSControlParameters, ie.MPTCPParameters,
			specTLV(ie.MPTCPAddressInformation, []byte{0}),
		},
		{
			message.MsgTypeSessionReportRequest, ie.SessionReport, ie.AccessAvailabilityReport,
			specTLV(ie.AccessAvailabilityInformation, []byte{0}),
		},
	}
	for _, test := range cases {
		payload := specTLV(test.root, specTLV(test.child, test.leaf))
		wire := protocolWire(t, test.messageType, true, 604, payload)
		prepared, ignored, err := preparePfcpPacket(wire)
		if err != nil || ignored != 0 || !bytes.Equal(prepared, wire) {
			t.Errorf("legal root=%d child=%d changed: ignored=%d err=%v", test.root, test.child, ignored, err)
		}
	}
}

func TestGroupedScopeGraphHasBoundedDepth(t *testing.T) {
	var visit func(ie.IEType, map[ie.IEType]bool, int)
	visit = func(parent ie.IEType, path map[ie.IEType]bool, depth int) {
		if path[parent] || depth > maxGroupedIEDepth {
			t.Fatalf("recursive or too deep grouped schema at %d", parent)
		}
		path[parent] = true
		for _, child := range nestedGroupedScopes[parent] {
			visit(child, path, depth+1)
		}
		delete(path, parent)
	}
	for _, roots := range messageGroupedScopes {
		for _, root := range roots {
			visit(root, make(map[ie.IEType]bool), 1)
		}
	}
}

func TestHeartbeatKeepsFirstTimestampAndSupportsDeepIgnoredIE(t *testing.T) {
	server := startProtocolTestServer(t)
	peer := specPeer(t, server)
	// First timestamp is valid; the later repetition is semantically invalid.
	specAssertHeartbeat(t, server, peer, 605, specTLV(ie.RecoveryTimeStamp, []byte{0}))
	specAssertHeartbeat(t, server, peer, 606, nestedGroupedPacket(16000)[8:])
}

func TestUnsupportedVersionIsHandledBeforeBody(t *testing.T) {
	server := startProtocolTestServer(t)
	peer := specPeer(t, server)
	packet := nestedGroupedPacket(16000)
	packet[0] = 0x40
	reply, err := specExchange(peer, packet)
	if err != nil {
		t.Fatal(err)
	}
	if reply.MessageType() != message.MsgTypeVersionNotSupportedResponse {
		t.Fatalf("response=%T", reply)
	}
}

func TestInvalidHeartbeatTimestampDoesNotReachTransaction(t *testing.T) {
	for _, timestamp := range []*ie.IE{nil, ie.New(ie.RecoveryTimeStamp, []byte{0})} {
		server := NewPfcpServer(nil, "127.0.0.1")
		bad, err := message.NewHeartbeatRequest(607, timestamp, nil).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		good, err := message.NewHeartbeatRequest(608, ie.NewRecoveryTimeStamp(time.Now()), nil).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		server.parseCh <- rawPfcpPacket{packet: bad}
		server.parseCh <- rawPfcpPacket{packet: good}
		var wg sync.WaitGroup
		wg.Add(1)
		go server.parser(&wg)
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case received := <-server.rcvCh:
			if received.Msg.Sequence() != 608 {
				t.Error("invalid heartbeat reached transaction layer")
			}
		case <-time.After(time.Second):
			t.Error("valid heartbeat was not processed")
		}
		server.Stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("parser did not stop")
		}
	}
}

func TestUnidentifiableAndUnknownPacketErrorsAreDiscarded(t *testing.T) {
	cases := [][]byte{
		nil,
		{0x20, message.MsgTypeAssociationUpdateRequest, 0, 0},
		{0x21, message.MsgTypeSessionReportRequest, 0, 4, 0, 0, 0, 0},
	}
	// An unknown message must not get a guessed response, even if its header
	// and an offending IE type can be read (TS 29.244 7.6.4).
	unknown := protocolWire(t, 255, false, 609, []byte{0, 1, 0, 1})
	cases = append(cases, unknown)
	for _, packet := range cases {
		server := NewPfcpServer(nil, "127.0.0.1")
		_, _, err := preparePfcpPacket(packet)
		if err == nil {
			t.Fatal("invalid test packet accepted")
		}
		server.queueParseError(rawPfcpPacket{packet: packet}, err)
		if len(server.rcvCh) != 0 {
			t.Error("unidentifiable or unknown message reached transactions")
		}
	}
}
