package processor

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func newSourceTestUPF(t *testing.T, ip string) (pfcptype.NodeID, *smf_context.UPF) {
	t.Helper()
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP(ip).To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })
	return nodeID, upf
}

func TestPassiveAssociationRejectsUnexpectedSource(t *testing.T) {
	wrongSources := map[string]*net.UDPAddr{
		"other host": {IP: net.ParseIP("198.51.100.7"), Port: 8805},
		"nil":        nil,
	}
	for name, source := range wrongSources {
		t.Run(name, func(t *testing.T) {
			nodeID, upf := newSourceTestUPF(t, "192.0.2.91")
			upf.EstablishAssociation(context.Background())
			t.Cleanup(upf.CancelAssociation)
			p := &Processor{}

			cause, afterResponse := p.SetupAssociation(nodeID, time.Now().Add(time.Hour), source)
			if cause != ie.CauseRequestRejected || afterResponse != nil {
				t.Fatalf("SetupAssociation() cause=%d callback=%t, want Request Rejected without callback",
					cause, afterResponse != nil)
			}

			cause, afterResponse = p.UpdateAssociation(nodeID, message.NewAssociationUpdateRequest(1,
				ie.NewPFCPAssociationReleaseRequest(1, 0)), source)
			if cause != ie.CauseRequestRejected || afterResponse != nil {
				t.Fatalf("UpdateAssociation(SARR) cause=%d callback=%t, want Request Rejected without callback",
					cause, afterResponse != nil)
			}

			if cause = p.ReleaseAssociation(nodeID, source); cause != ie.CauseRequestRejected {
				t.Fatalf("ReleaseAssociation() cause=%d, want Request Rejected", cause)
			}

			if err := upf.IsAssociated(); err != nil {
				t.Fatalf("association changed after rejected requests: %v", err)
			}
		})
	}
}

// The source check runs after the UPF lookup, so the protocol Cause for an
// unknown or unassociated UPF does not depend on the source address.
func TestPassiveAssociationKeepsCauseForUnknownOrUnassociatedUPF(t *testing.T) {
	wrongSource := &net.UDPAddr{IP: net.ParseIP("198.51.100.7"), Port: 8805}
	p := &Processor{}

	unknown := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.92").To4(),
	}
	if cause, _ := p.SetupAssociation(unknown, time.Now(), wrongSource); cause != ie.CauseRequestRejected {
		t.Fatalf("SetupAssociation(unknown) cause=%d, want Request Rejected", cause)
	}
	if cause, _ := p.UpdateAssociation(unknown, message.NewAssociationUpdateRequest(1), wrongSource); cause !=
		ie.CauseNoEstablishedPFCPAssociation {
		t.Fatalf("UpdateAssociation(unknown) cause=%d, want No Established PFCP Association", cause)
	}
	if cause := p.ReleaseAssociation(unknown, wrongSource); cause != ie.CauseNoEstablishedPFCPAssociation {
		t.Fatalf("ReleaseAssociation(unknown) cause=%d, want No Established PFCP Association", cause)
	}

	unassociated, _ := newSourceTestUPF(t, "192.0.2.93")
	if cause, _ := p.UpdateAssociation(unassociated, message.NewAssociationUpdateRequest(1), wrongSource); cause !=
		ie.CauseNoEstablishedPFCPAssociation {
		t.Fatalf("UpdateAssociation(unassociated) cause=%d, want No Established PFCP Association", cause)
	}
}
