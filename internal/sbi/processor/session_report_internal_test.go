package processor

import (
	"context"
	"net"
	"testing"

	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
)

func TestSessionReportRequestFromUPFWithoutAssociation(t *testing.T) {
	_, settingUp := newSourceTestUPF(t, "192.0.2.94")
	if !settingUp.BeginAssociationSetup() {
		t.Fatal("BeginAssociationSetup() was rejected")
	}
	t.Cleanup(settingUp.FailAssociationSetup)
	newSourceTestUPF(t, "192.0.2.96")
	_, associated := newSourceTestUPF(t, "192.0.2.95")
	associated.EstablishAssociation(context.Background())
	t.Cleanup(associated.CancelAssociation)
	p := &Processor{}

	testCases := []struct {
		name   string
		remote *net.UDPAddr
		cause  uint8
	}{
		{"UPF setting up", &net.UDPAddr{IP: net.ParseIP("192.0.2.94"), Port: 8805}, ie.CauseNoEstablishedPFCPAssociation},
		{"UPF down", &net.UDPAddr{IP: net.ParseIP("192.0.2.96"), Port: 8805}, ie.CauseNoEstablishedPFCPAssociation},
		{"associated UPF", &net.UDPAddr{IP: net.ParseIP("192.0.2.95"), Port: 8805}, ie.CauseSessionContextNotFound},
		// An associated UPF may send from an F-SEID address other than its
		// Node ID's (TS 29.244 5.8.1).
		{"other address", &net.UDPAddr{IP: net.ParseIP("198.51.100.7"), Port: 8805}, ie.CauseSessionContextNotFound},
		{"nil source", nil, ie.CauseSessionContextNotFound},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := message.NewSessionReportRequest(0, 0, 0xdeadbeef, 1, 0, ie.NewReportType(0, 0, 1, 0))
			cause, remoteSEID := p.HandleSessionReportRequest(request, testCase.remote)
			if cause != testCase.cause || remoteSEID != 0 {
				t.Fatalf("HandleSessionReportRequest() cause=%d SEID=%d, want cause %d SEID 0",
					cause, remoteSEID, testCase.cause)
			}
		})
	}
}
