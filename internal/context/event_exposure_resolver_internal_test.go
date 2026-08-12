package context

import (
	stdctx "context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/free5gc/openapi/models"
)

const mutatedSnssaiSd = "ffffff"

func TestEventExposureResolverResolvesActiveSelectedUPFOnly(t *testing.T) {
	resetEventExposureResolverState(t)
	selectedUPF := testEventExposureUPF("http://selected.example.com/api")
	otherUPF := testEventExposureUPF("http://other.example.com/api")
	smfContext.UserPlaneInformation = &UserPlaneInformation{
		UPFs: map[string]*UPNode{
			"selected": selectedUPF,
			"other":    otherUPF,
		},
	}
	smContext := newEventExposureSMContext(t, "imsi-001010000000001", 0, Active, selectedUPF)

	target, err := NewEventExposureTargetResolver().ResolveEventExposureTarget(
		stdctx.Background(),
		"imsi-001010000000001",
		EventExposureSelectors{
			PDUSessionID: int32Ptr(0),
			Dnn:          stringPtr("internet"),
			Snssai:       &models.Snssai{Sst: 1, Sd: "010203"},
		},
	)
	if err != nil {
		t.Fatalf("ResolveEventExposureTarget failed: %v", err)
	}
	if target.APIroot != selectedUPF.NupfEeApiRoot ||
		target.ServiceBaseURL != selectedUPF.NupfEeApiRoot+"/nupf-ee/v1" ||
		target.PDUSessionID != smContext.PDUSessionID ||
		target.Dnn != smContext.Dnn ||
		!target.UEIPAddress.Equal(smContext.PDUAddress) {
		t.Fatalf("unexpected target snapshot: %+v", target)
	}
}

func TestEventExposureResolverRejectsInactiveAndMismatchedSelectors(t *testing.T) {
	tests := []struct {
		name      string
		state     SMContextState
		selectors EventExposureSelectors
	}{
		{
			name:  "inactive",
			state: InActive,
		},
		{
			name:  "dnn mismatch",
			state: Active,
			selectors: EventExposureSelectors{
				Dnn: stringPtr("ims"),
			},
		},
		{
			name:  "snssai mismatch",
			state: Active,
			selectors: EventExposureSelectors{
				Snssai: &models.Snssai{Sst: 1, Sd: mutatedSnssaiSd},
			},
		},
		{
			name:  "pdu session mismatch",
			state: Active,
			selectors: EventExposureSelectors{
				PDUSessionID: int32Ptr(1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetEventExposureResolverState(t)
			upf := testEventExposureUPF("http://upf.example.com/api")
			smfContext.UserPlaneInformation = &UserPlaneInformation{UPFs: map[string]*UPNode{"upf": upf}}
			newEventExposureSMContext(t, "imsi-001010000000001", 0, tt.state, upf)

			_, err := NewEventExposureTargetResolver().ResolveEventExposureTarget(
				stdctx.Background(), "imsi-001010000000001", tt.selectors)
			if !errors.Is(err, ErrEventExposureNoMatchingSession) {
				t.Fatalf("expected no matching session, got %v", err)
			}
		})
	}
}

func TestEventExposureResolverAmbiguityAndMissingDependencies(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T)
		wantErr error
	}{
		{
			name: "multiple matching sessions",
			setup: func(t *testing.T) {
				upf := testEventExposureUPF("http://upf.example.com/api")
				smfContext.UserPlaneInformation = &UserPlaneInformation{UPFs: map[string]*UPNode{"upf": upf}}
				newEventExposureSMContext(t, "imsi-001010000000001", 1, Active, upf)
				newEventExposureSMContext(t, "imsi-001010000000001", 2, Active, upf)
			},
			wantErr: ErrEventExposureMultipleSessions,
		},
		{
			name: "missing UE IP",
			setup: func(t *testing.T) {
				upf := testEventExposureUPF("http://upf.example.com/api")
				smfContext.UserPlaneInformation = &UserPlaneInformation{UPFs: map[string]*UPNode{"upf": upf}}
				smContext := newEventExposureSMContext(t, "imsi-001010000000001", 1, Active, upf)
				smContext.PDUAddress = nil
			},
			wantErr: ErrEventExposureMissingUEIP,
		},
		{
			name: "missing selected UPF",
			setup: func(t *testing.T) {
				smfContext.UserPlaneInformation = &UserPlaneInformation{UPFs: map[string]*UPNode{}}
				newEventExposureSMContext(t, "imsi-001010000000001", 1, Active, nil)
			},
			wantErr: ErrEventExposureMissingUPF,
		},
		{
			name: "missing endpoint",
			setup: func(t *testing.T) {
				upf := testEventExposureUPF("")
				smfContext.UserPlaneInformation = &UserPlaneInformation{UPFs: map[string]*UPNode{"upf": upf}}
				newEventExposureSMContext(t, "imsi-001010000000001", 1, Active, upf)
			},
			wantErr: ErrEventExposureMissingEndpoint,
		},
		{
			name: "selected UPF pointer absent from topology",
			setup: func(t *testing.T) {
				selectedUPF := testEventExposureUPF("http://selected.example.com/api")
				otherUPF := testEventExposureUPF("http://other.example.com/api")
				smfContext.UserPlaneInformation = &UserPlaneInformation{UPFs: map[string]*UPNode{"other": otherUPF}}
				newEventExposureSMContext(t, "imsi-001010000000001", 1, Active, selectedUPF)
			},
			wantErr: ErrEventExposureMissingUPF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetEventExposureResolverState(t)
			tt.setup(t)
			_, err := NewEventExposureTargetResolver().ResolveEventExposureTarget(
				stdctx.Background(), "imsi-001010000000001", EventExposureSelectors{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error mismatch: got %v want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEventExposureResolverCopiesSnapshotValues(t *testing.T) {
	resetEventExposureResolverState(t)
	upf := testEventExposureUPF("http://upf.example.com/api")
	smfContext.UserPlaneInformation = &UserPlaneInformation{UPFs: map[string]*UPNode{"upf": upf}}
	smContext := newEventExposureSMContext(t, "imsi-001010000000001", 1, Active, upf)

	target, err := NewEventExposureTargetResolver().ResolveEventExposureTarget(
		stdctx.Background(), "imsi-001010000000001", EventExposureSelectors{})
	if err != nil {
		t.Fatalf("ResolveEventExposureTarget failed: %v", err)
	}

	smContext.PDUAddress[0] = 203
	upf.NupfEeApiRoot = "http://mutated.example.com"
	smContext.SNssai.Sd = mutatedSnssaiSd
	if !target.UEIPAddress.Equal(net.ParseIP("192.0.2.1")) ||
		target.APIroot != "http://upf.example.com/api" ||
		target.Snssai.Sd != "010203" {
		t.Fatalf("target aliases mutable state: %+v", target)
	}
}

func resetEventExposureResolverState(t *testing.T) {
	t.Helper()
	oldUPI := smfContext.UserPlaneInformation
	smfContext.UserPlaneInformation = nil
	smContextPool.Range(func(key, _ interface{}) bool {
		smContextPool.Delete(key)
		return true
	})
	canonicalRef.Range(func(key, _ interface{}) bool {
		canonicalRef.Delete(key)
		return true
	})
	t.Cleanup(func() {
		smContextPool.Range(func(key, _ interface{}) bool {
			smContextPool.Delete(key)
			return true
		})
		canonicalRef.Range(func(key, _ interface{}) bool {
			canonicalRef.Delete(key)
			return true
		})
		smfContext.UserPlaneInformation = oldUPI
	})
}

func newEventExposureSMContext(
	t *testing.T,
	supi string,
	pduSessionID int32,
	state SMContextState,
	selectedUPF *UPNode,
) *SMContext {
	t.Helper()
	smContext := &SMContext{
		Ref:          fmt.Sprintf("%s-%d", supi, pduSessionID),
		Identifier:   supi,
		PDUSessionID: pduSessionID,
		SmfPduSessionSmContextCreateData: &models.SmfPduSessionSmContextCreateData{
			Supi:   supi,
			Dnn:    "internet",
			SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
		},
		PDUAddress:  net.ParseIP("192.0.2.1").To4(),
		SelectedUPF: selectedUPF,
		state:       state,
	}
	smContextPool.Store(smContext.Ref, smContext)
	return smContext
}

func testEventExposureUPF(apiRoot string) *UPNode {
	return &UPNode{
		Type:          UPNODE_UPF,
		NupfEeApiRoot: apiRoot,
		UPF:           &UPF{},
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}

func stringPtr(value string) *string {
	return &value
}
