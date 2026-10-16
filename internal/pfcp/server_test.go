package pfcp_test

import (
	"net"
	"testing"

	"github.com/free5gc/smf/internal/pfcp"
)

func TestTransactionIDIgnoresUDPPort(t *testing.T) {
	first := &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 8805}
	second := &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 18805}

	firstID := pfcp.TransactionID(first, 0x123456)
	secondID := pfcp.TransactionID(second, 0x123456)
	if firstID != secondID {
		t.Fatalf("transaction IDs differ only because of UDP port: %q != %q", firstID, secondID)
	}

	otherPeer := &net.UDPAddr{IP: net.ParseIP("192.0.2.11"), Port: 8805}
	if firstID == pfcp.TransactionID(otherPeer, 0x123456) {
		t.Fatal("transaction IDs for different peer IPs must differ")
	}
	if firstID == pfcp.TransactionID(first, 0x123457) {
		t.Fatal("transaction IDs for different sequence numbers must differ")
	}
}
