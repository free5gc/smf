package pfcptype_test

import (
	"net"
	"testing"

	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func TestNodeIDString(t *testing.T) {
	tests := []struct {
		name string
		node pfcptype.NodeID
		want string
	}{
		{
			name: "IPv4",
			node: pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("10.0.0.1").To4()},
			want: "10.0.0.1",
		},
		{
			name: "IPv6",
			node: pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv6Address, IP: net.ParseIP("2001:db8::1")},
			want: "2001:db8::1",
		},
		{
			name: "FQDN",
			node: pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeFqdn, FQDN: "upf.example.com"},
			want: "upf.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.node.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNodeIDEqualsTo(t *testing.T) {
	a := &pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("10.0.0.1").To4()}
	b := &pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("10.0.0.1").To4()}
	c := &pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("10.0.0.2").To4()}
	fqdn := &pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeFqdn, FQDN: "10.0.0.1"}

	if !a.EqualsTo(b) {
		t.Fatal("expected equal IPv4 NodeIDs")
	}
	if a.EqualsTo(c) {
		t.Fatal("expected different IPv4 NodeIDs")
	}
	if a.EqualsTo(fqdn) {
		t.Fatal("expected NodeIDs with different types to differ")
	}
	if a.EqualsTo(nil) {
		t.Fatal("expected a non-nil pfcptype.NodeID to differ from nil")
	}
}

func TestReportingTriggerIEUsesR15TwoOctetForm(t *testing.T) {
	r := &pfcptype.ReportingTrigger{}
	r.SetPERIO()
	r.SetSTART()

	got, err := r.IE().ReportingTriggers()
	if err != nil {
		t.Fatalf("ReportingTriggers() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ReportingTriggers() length = %d, want 2: %v", len(got), got)
	}
	if got[0] != byte(pfcptype.RPT_TRIG_PERIO|pfcptype.RPT_TRIG_START) || got[1] != 0 {
		t.Fatalf("ReportingTriggers() = %v, want [%d 0]", got, pfcptype.RPT_TRIG_PERIO|pfcptype.RPT_TRIG_START)
	}
}

func TestReportingTriggerIEIncludesSecondAndThirdOctets(t *testing.T) {
	t.Run("second octet", func(t *testing.T) {
		r := &pfcptype.ReportingTrigger{}
		r.SetVOLQU()

		got, err := r.IE().ReportingTriggers()
		if err != nil {
			t.Fatalf("ReportingTriggers() error: %v", err)
		}
		if len(got) != 2 || got[1] == 0 {
			t.Fatalf("ReportingTriggers() = %v, want a non-zero second octet", got)
		}
	})

	t.Run("third octet", func(t *testing.T) {
		r := &pfcptype.ReportingTrigger{}
		r.SetUPINT()

		got, err := r.IE().ReportingTriggers()
		if err != nil {
			t.Fatalf("ReportingTriggers() error: %v", err)
		}
		if len(got) != 3 || got[2] == 0 {
			t.Fatalf("ReportingTriggers() = %v, want a non-zero third octet", got)
		}
	})
}

func TestOuterHeaderCreationDescriptionsUseWireRepresentation(t *testing.T) {
	tests := []struct {
		name string
		got  uint16
		want uint16
	}{
		{name: "GTP-U UDP IPv4", got: pfcptype.OuterHeaderCreationGtpUUdpIpv4, want: 0x0100},
		{name: "GTP-U UDP IPv6", got: pfcptype.OuterHeaderCreationGtpUUdpIpv6, want: 0x0200},
		{name: "UDP IPv4", got: pfcptype.OuterHeaderCreationUdpIpv4, want: 0x0400},
		{name: "UDP IPv6", got: pfcptype.OuterHeaderCreationUdpIpv6, want: 0x0800},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("description = %#04x, want %#04x", test.got, test.want)
			}
		})
	}
}
