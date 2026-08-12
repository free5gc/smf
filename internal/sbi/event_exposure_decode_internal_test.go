package sbi

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin/binding"
)

func TestDecodeEventExposureCreateRequestDnnPresence(t *testing.T) {
	tests := []struct {
		name       string
		patch      string
		wantDnnSet bool
		wantDnn    string
		wantErr    bool
	}{
		{
			name:       "omitted dnn",
			patch:      "",
			wantDnnSet: false,
		},
		{
			name:    "empty dnn",
			patch:   `"dnn":"",`,
			wantErr: true,
		},
		{
			name:       "non-empty dnn",
			patch:      `"dnn":"internet",`,
			wantDnnSet: true,
			wantDnn:    "internet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, problemDetails := decodeEventExposureCreateRequest(strings.NewReader(validEventExposureJSON(tt.patch)))
			if tt.wantErr {
				if problemDetails == nil {
					t.Fatal("expected ProblemDetails")
				}
				return
			}
			if problemDetails != nil {
				t.Fatalf("unexpected ProblemDetails: %+v", problemDetails)
			}
			if (request.Selectors.Dnn != nil) != tt.wantDnnSet {
				t.Fatalf("Dnn presence mismatch: got %v", request.Selectors.Dnn != nil)
			}
			if request.Selectors.Dnn != nil && *request.Selectors.Dnn != tt.wantDnn {
				t.Fatalf("Dnn mismatch: got %q want %q", *request.Selectors.Dnn, tt.wantDnn)
			}
		})
	}
}

func TestDecodeEventExposureCreateRequestRejectsUnsupportedExactWireNames(t *testing.T) {
	for _, field := range []string{
		`"ImmeRep":true,`,
		`"serviveName":"nsmf-event-exposure",`,
	} {
		_, problemDetails := decodeEventExposureCreateRequest(strings.NewReader(validEventExposureJSON(field)))
		if problemDetails == nil {
			t.Fatalf("expected ProblemDetails for field patch %s", field)
		}
	}
	for _, field := range []string{
		`"bundlingAllowed":false,`,
		`"bundlingAllowed":true,`,
		`"bundleId":0,`,
		`"bundleId":7,`,
	} {
		_, problemDetails := decodeEventExposureCreateRequest(strings.NewReader(validEventExposureJSONWithEventPatch(field)))
		if problemDetails == nil {
			t.Fatalf("expected ProblemDetails for event field patch %s", field)
		}
	}
}

func TestDecodeEventExposureCreateRequestPreservesExplicitZeroPDUSessionID(t *testing.T) {
	request, problemDetails := decodeEventExposureCreateRequest(strings.NewReader(validEventExposureJSON(`"pduSeId":0,`)))
	if problemDetails != nil {
		t.Fatalf("unexpected ProblemDetails: %+v", problemDetails)
	}
	if request.Selectors.PDUSessionID == nil || *request.Selectors.PDUSessionID != 0 {
		t.Fatalf("expected explicit pduSeId 0, got %+v", request.Selectors.PDUSessionID)
	}
}

func TestDecodeEventExposureCreateRequestRejectsMultiEntryProfile(t *testing.T) {
	body := strings.Replace(validEventExposureJSON(""), `"eventSubs":[`, `"eventSubs":[{},`, 1)
	_, problemDetails := decodeEventExposureCreateRequest(strings.NewReader(body))
	if problemDetails == nil {
		t.Fatal("expected ProblemDetails for multiple eventSubs")
	}
}

func TestDecodeEventExposureCreateRequestRejectsRequiredFieldFailures(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantParam string
	}{
		{
			name:      "missing supi",
			body:      strings.Replace(validEventExposureJSON(""), `"supi":"imsi-001010000000001",`, "", 1),
			wantParam: "/supi",
		},
		{
			name:      "missing notifId",
			body:      strings.Replace(validEventExposureJSON(""), `"notifId":"correlation-1",`, "", 1),
			wantParam: "/notifId",
		},
		{
			name:      "missing notifUri",
			body:      strings.Replace(validEventExposureJSON(""), `"notifUri":"https://nwdaf.example.com/nsmf",`, "", 1),
			wantParam: "/notifUri",
		},
		{
			name:      "missing eventSubs",
			body:      strings.Replace(validEventExposureJSON(""), `"eventSubs":[{`, `"eventSubsMissing":[{`, 1),
			wantParam: "/",
		},
		{
			name:      "missing upfEvents",
			body:      strings.Replace(validEventExposureJSON(""), `"upfEvents":[{`, `"upfEventsMissing":[{`, 1),
			wantParam: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeProblemParam(t, tt.body, tt.wantParam)
		})
	}
}

func TestDecodeEventExposureCreateRequestRejectsInvalidURIAndReportingFields(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantParam string
	}{
		{
			name: "relative notifUri",
			body: validEventExposureJSONWithReplacement(
				`"notifUri":"https://nwdaf.example.com/nsmf"`, `"notifUri":"/nsmf"`),
			wantParam: "/notifUri",
		},
		{
			name: "userinfo notifUri",
			body: validEventExposureJSONWithReplacement(
				`"notifUri":"https://nwdaf.example.com/nsmf"`,
				`"notifUri":"https://u@nwdaf.example.com/nsmf"`),
			wantParam: "/notifUri",
		},
		{
			name: "fragment bundled uri",
			body: validEventExposureJSONWithReplacement(
				`"bundledEventNotifyUri":"https://nwdaf.example.com/upf"`,
				`"bundledEventNotifyUri":"https://nwdaf.example.com/upf#x"`),
			wantParam: "/eventSubs/0/bundledEventNotifyUri",
		},
		{
			name:      "missing repPeriod",
			body:      strings.Replace(validEventExposureJSON(""), `"repPeriod":10,`, "", 1),
			wantParam: "/repPeriod",
		},
		{
			name:      "zero repPeriod",
			body:      validEventExposureJSONWithReplacement(`"repPeriod":10`, `"repPeriod":0`),
			wantParam: "/repPeriod",
		},
		{
			name:      "negative repPeriod",
			body:      validEventExposureJSONWithReplacement(`"repPeriod":10`, `"repPeriod":-1`),
			wantParam: "/repPeriod",
		},
		{
			name:      "negative pduSeId",
			body:      validEventExposureJSON(`"pduSeId":-1,`),
			wantParam: "/pduSeId",
		},
		{
			name:      "too large pduSeId",
			body:      validEventExposureJSON(`"pduSeId":256,`),
			wantParam: "/pduSeId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeProblemParam(t, tt.body, tt.wantParam)
		})
	}
}

func TestDecodeEventExposureCreateRequestRejectsUnsupportedProfileValues(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantParam string
	}{
		{
			name:      "unsupported event",
			body:      validEventExposureJSONWithReplacement(`"event":"UPF_EVENT"`, `"event":"PDU_SES_EST"`),
			wantParam: "/eventSubs/0/event",
		},
		{
			name:      "unsupported measurement",
			body:      validEventExposureJSONWithReplacement(`"VOLUME_MEASUREMENT"`, `"DURATION_MEASUREMENT"`),
			wantParam: "/eventSubs/0/upfEvents/0/measurementTypes/0",
		},
		{
			name: "missing measurement",
			body: validEventExposureJSONWithReplacement(
				`"measurementTypes":["VOLUME_MEASUREMENT"]`, `"measurementTypes":[]`),
			wantParam: "/eventSubs/0/upfEvents/0/measurementTypes",
		},
		{
			name: "unsupported granularity",
			body: validEventExposureJSONWithReplacement(
				`"granularityOfMeasurement":"PER_SESSION"`, `"granularityOfMeasurement":"PER_FLOW"`),
			wantParam: "/eventSubs/0/upfEvents/0/granularityOfMeasurement",
		},
		{
			name:      "unsupported notifMethod",
			body:      validEventExposureJSON(`"notifMethod":"ONE_TIME",`),
			wantParam: "/notifMethod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeProblemParam(t, tt.body, tt.wantParam)
		})
	}
}

func TestDecodeEventExposureCreateRequestRejectsUnknownTrailingAndNormalizedWrongNames(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantParam string
	}{
		{
			name:      "unknown field",
			body:      validEventExposureJSON(`"unexpected":true,`),
			wantParam: "/",
		},
		{
			name:      "trailing json",
			body:      validEventExposureJSON("") + `{}`,
			wantParam: "/",
		},
		{
			name:      "normalized ImmeRep rejected",
			body:      validEventExposureJSON(`"immeRep":true,`),
			wantParam: "/ImmeRep",
		},
		{
			name:      "normalized serviveName rejected",
			body:      validEventExposureJSON(`"serviceName":"nsmf-event-exposure",`),
			wantParam: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeProblemParam(t, tt.body, tt.wantParam)
		})
	}
}

func TestDecodeEventExposureCreateRequestDoesNotChangeGinGlobalDecoderFlags(t *testing.T) {
	originalDisallowUnknown := binding.EnableDecoderDisallowUnknownFields
	originalUseNumber := binding.EnableDecoderUseNumber
	t.Cleanup(func() {
		binding.EnableDecoderDisallowUnknownFields = originalDisallowUnknown
		binding.EnableDecoderUseNumber = originalUseNumber
	})

	_, problemDetails := decodeEventExposureCreateRequest(strings.NewReader(validEventExposureJSON("")))
	if problemDetails != nil {
		t.Fatalf("unexpected ProblemDetails: %+v", problemDetails)
	}
	if binding.EnableDecoderDisallowUnknownFields != originalDisallowUnknown ||
		binding.EnableDecoderUseNumber != originalUseNumber {
		t.Fatal("route-local decoder changed Gin global decoder flags")
	}
}

func assertDecodeProblemParam(t *testing.T, body, wantParam string) {
	t.Helper()
	_, problemDetails := decodeEventExposureCreateRequest(strings.NewReader(body))
	if problemDetails == nil {
		t.Fatal("expected ProblemDetails")
	}
	if problemDetails.Status != 400 {
		t.Fatalf("status mismatch: %d", problemDetails.Status)
	}
	if len(problemDetails.InvalidParams) == 0 || problemDetails.InvalidParams[0].Param != wantParam {
		t.Fatalf("param mismatch: got %+v want %s", problemDetails.InvalidParams, wantParam)
	}
}

func validEventExposureJSONWithReplacement(old, replacement string) string {
	return strings.Replace(validEventExposureJSON(""), old, replacement, 1)
}

func validEventExposureJSON(extraTopLevel string) string {
	return validEventExposureJSONWithPatches(extraTopLevel, "")
}

func validEventExposureJSONWithEventPatch(extraEvent string) string {
	return validEventExposureJSONWithPatches("", extraEvent)
}

func validEventExposureJSONWithPatches(extraTopLevel, extraEvent string) string {
	return `{
		` + extraTopLevel + `
		"supi":"imsi-001010000000001",
		"notifId":"correlation-1",
		"notifUri":"https://nwdaf.example.com/nsmf",
		"repPeriod":10,
		"eventSubs":[{
			` + extraEvent + `
			"event":"UPF_EVENT",
			"bundledEventNotifyUri":"https://nwdaf.example.com/upf",
			"upfEvents":[{
				"type":"USER_DATA_USAGE_MEASURES",
				"measurementTypes":["VOLUME_MEASUREMENT"],
				"granularityOfMeasurement":"PER_SESSION"
			}]
		}]
	}`
}
