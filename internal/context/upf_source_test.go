package context_test

import (
	"net"
	"testing"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func TestUPFMatchesPFCPSource(t *testing.T) {
	ipv4 := &smf_context.UPF{NodeID: pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.1").To4(),
	}}
	ipv6 := &smf_context.UPF{NodeID: pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv6Address,
		IP:         net.ParseIP("2001:db8::1"),
	}}
	unresolvable := &smf_context.UPF{NodeID: pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeFqdn,
		FQDN:       "upf.invalid",
	}}

	tests := []struct {
		name   string
		upf    *smf_context.UPF
		source net.IP
		want   bool
	}{
		{"same IPv4", ipv4, net.ParseIP("192.0.2.1"), true},
		{"IPv4-mapped IPv6 of the same address", ipv4, net.ParseIP("::ffff:192.0.2.1"), true},
		{"different IPv4", ipv4, net.ParseIP("192.0.2.2"), false},
		{"same IPv6", ipv6, net.ParseIP("2001:db8::1"), true},
		{"different IPv6", ipv6, net.ParseIP("2001:db8::2"), false},
		{"nil source", ipv4, nil, false},
		{"unspecified source", ipv4, net.IPv4zero, false},
		{"nil UPF", nil, net.ParseIP("192.0.2.1"), false},
		{"FQDN that does not resolve", unresolvable, net.ParseIP("192.0.2.1"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.upf.MatchesPFCPSource(test.source); got != test.want {
				t.Fatalf("MatchesPFCPSource(%v) = %t, want %t", test.source, got, test.want)
			}
		})
	}
}

func TestUPFMatchesPFCPSourceAcceptsAnyResolvedAddress(t *testing.T) {
	addresses, err := net.DefaultResolver.LookupIP(t.Context(), "ip", "localhost")
	if err != nil || len(addresses) == 0 {
		t.Skipf("localhost does not resolve here: %v", err)
	}
	upf := &smf_context.UPF{NodeID: pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeFqdn,
		FQDN:       "localhost",
	}}
	for _, address := range addresses {
		if !upf.MatchesPFCPSource(address) {
			t.Errorf("MatchesPFCPSource(%v) = false for a resolved address of localhost", address)
		}
	}
	if upf.MatchesPFCPSource(net.ParseIP("192.0.2.1")) {
		t.Error("MatchesPFCPSource accepted an address localhost does not resolve to")
	}
}
