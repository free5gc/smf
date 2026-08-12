package sbi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/processor"
)

type presentString struct {
	Set   bool
	Value string
}

func (p *presentString) UnmarshalJSON(data []byte) error {
	p.Set = true
	return json.Unmarshal(data, &p.Value)
}

type presentInt32 struct {
	Set   bool
	Value int32
}

func (p *presentInt32) UnmarshalJSON(data []byte) error {
	p.Set = true
	return json.Unmarshal(data, &p.Value)
}

type eventExposureCreateBody struct {
	Supi              presentString                              `json:"supi"`
	Gpsi              *json.RawMessage                           `json:"gpsi,omitempty"`
	AnyUeInd          *json.RawMessage                           `json:"anyUeInd,omitempty"`
	GroupId           *json.RawMessage                           `json:"groupId,omitempty"`
	PduSeId           presentInt32                               `json:"pduSeId"`
	Dnn               presentString                              `json:"dnn"`
	Snssai            *models.Snssai                             `json:"snssai,omitempty"`
	SubId             *json.RawMessage                           `json:"subId,omitempty"`
	NotifId           presentString                              `json:"notifId"`
	NotifUri          presentString                              `json:"notifUri"`
	AltNotifIpv4Addrs *json.RawMessage                           `json:"altNotifIpv4Addrs,omitempty"`
	AltNotifIpv6Addrs *json.RawMessage                           `json:"altNotifIpv6Addrs,omitempty"`
	AltNotifFqdns     *json.RawMessage                           `json:"altNotifFqdns,omitempty"`
	EventSubs         []eventExposureEventSubscriptionBody       `json:"eventSubs"`
	EventNotifs       *json.RawMessage                           `json:"eventNotifs,omitempty"`
	ImmeRep           *json.RawMessage                           `json:"ImmeRep,omitempty"`
	NotifMethod       *models.SmfEventExposureNotificationMethod `json:"notifMethod,omitempty"`
	MaxReportNbr      *json.RawMessage                           `json:"maxReportNbr,omitempty"`
	Expiry            *json.RawMessage                           `json:"expiry,omitempty"`
	RepPeriod         presentInt32                               `json:"repPeriod"`
	Guami             *json.RawMessage                           `json:"guami,omitempty"`
	ServiveName       *json.RawMessage                           `json:"serviveName,omitempty"`
	SupportedFeatures *json.RawMessage                           `json:"supportedFeatures,omitempty"`
	SampRatio         *json.RawMessage                           `json:"sampRatio,omitempty"`
	PartitionCriteria *json.RawMessage                           `json:"partitionCriteria,omitempty"`
	GrpRepTime        *json.RawMessage                           `json:"grpRepTime,omitempty"`
	NotifFlag         *json.RawMessage                           `json:"notifFlag,omitempty"`
}

type eventExposureEventSubscriptionBody struct {
	Event                 models.SmfEvent             `json:"event"`
	DnaiChgType           *json.RawMessage            `json:"dnaiChgType,omitempty"`
	DddTraDescriptors     *json.RawMessage            `json:"dddTraDescriptors,omitempty"`
	DddStati              *json.RawMessage            `json:"dddStati,omitempty"`
	AppIds                *json.RawMessage            `json:"appIds,omitempty"`
	TargetPeriod          *json.RawMessage            `json:"targetPeriod,omitempty"`
	TransacDispInd        *json.RawMessage            `json:"transacDispInd,omitempty"`
	TransacMetrics        *json.RawMessage            `json:"transacMetrics,omitempty"`
	UeIpAddr              *json.RawMessage            `json:"ueIpAddr,omitempty"`
	UpfEvents             []eventExposureUPFEventBody `json:"upfEvents"`
	BundlingAllowed       *json.RawMessage            `json:"bundlingAllowed,omitempty"`
	BundleId              *json.RawMessage            `json:"bundleId,omitempty"`
	BundledEventNotifyUri presentString               `json:"bundledEventNotifyUri"`
}

type eventExposureUPFEventBody struct {
	Type                     models.UpfEventType                `json:"type"`
	MeasurementTypes         []models.UpfMeasurementType        `json:"measurementTypes"`
	GranularityOfMeasurement models.UpfGranularityOfMeasurement `json:"granularityOfMeasurement"`
}

func decodeEventExposureCreateRequest(r io.Reader) (processor.EventExposureCreateRequest, *models.ProblemDetails) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()

	var body eventExposureCreateBody
	if err := decoder.Decode(&body); err != nil {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/",
			"malformed JSON or unsupported field")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/", "trailing JSON is not allowed")
	}

	return validateEventExposureCreateBody(body)
}

func validateEventExposureCreateBody(
	body eventExposureCreateBody,
) (processor.EventExposureCreateRequest, *models.ProblemDetails) {
	if field := unsupportedTopLevelField(body); field != "" {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/"+field, "unsupported field")
	}
	if !body.Supi.Set || body.Supi.Value == "" {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/supi", "required")
	}
	if !body.NotifId.Set || body.NotifId.Value == "" {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/notifId", "required")
	}
	if !body.NotifUri.Set || !validEventExposureCallbackURI(body.NotifUri.Value) {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/notifUri", "invalid URI")
	}
	if !body.RepPeriod.Set || body.RepPeriod.Value <= 0 {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/repPeriod", "must be positive")
	}
	if body.PduSeId.Set && (body.PduSeId.Value < 0 || body.PduSeId.Value > 255) {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/pduSeId", "out of range")
	}
	if body.Dnn.Set && body.Dnn.Value == "" {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/dnn", "must not be empty")
	}
	if body.NotifMethod != nil && *body.NotifMethod != models.SmfEventExposureNotificationMethod_PERIODIC {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem("/notifMethod", "unsupported value")
	}
	if len(body.EventSubs) != 1 {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs", "must contain exactly one entry")
	}

	eventSub := body.EventSubs[0]
	if field := unsupportedEventField(eventSub); field != "" {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs/0/"+field, "unsupported field")
	}
	if eventSub.Event != models.SmfEvent_UPF_EVENT {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs/0/event", "unsupported value")
	}
	if !eventSub.BundledEventNotifyUri.Set || !validEventExposureCallbackURI(eventSub.BundledEventNotifyUri.Value) {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs/0/bundledEventNotifyUri", "invalid URI")
	}
	if len(eventSub.UpfEvents) != 1 {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs/0/upfEvents", "must contain exactly one entry")
	}

	upfEvent := eventSub.UpfEvents[0]
	if upfEvent.Type != models.UpfEventType_USER_DATA_USAGE_MEASURES {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs/0/upfEvents/0/type", "unsupported value")
	}
	if len(upfEvent.MeasurementTypes) == 0 {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs/0/upfEvents/0/measurementTypes", "required")
	}
	for i, measurementType := range upfEvent.MeasurementTypes {
		if measurementType != models.UpfMeasurementType_VOLUME_MEASUREMENT &&
			measurementType != models.UpfMeasurementType_THROUGHPUT_MEASUREMENT {
			return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
				fmt.Sprintf("/eventSubs/0/upfEvents/0/measurementTypes/%d", i), "unsupported value")
		}
	}
	if upfEvent.GranularityOfMeasurement != models.UpfGranularityOfMeasurement_PER_SESSION {
		return processor.EventExposureCreateRequest{}, malformedEventExposureProblem(
			"/eventSubs/0/upfEvents/0/granularityOfMeasurement", "unsupported value")
	}

	selectors := smf_context.EventExposureSelectors{}
	if body.PduSeId.Set {
		pduSessionID := body.PduSeId.Value
		selectors.PDUSessionID = &pduSessionID
	}
	if body.Dnn.Set {
		dnn := body.Dnn.Value
		selectors.Dnn = &dnn
	}
	if body.Snssai != nil {
		selectors.Snssai = &models.Snssai{Sst: body.Snssai.Sst, Sd: body.Snssai.Sd}
	}

	return processor.EventExposureCreateRequest{
		Supi:                  body.Supi.Value,
		Selectors:             selectors,
		NotifID:               body.NotifId.Value,
		NotifURI:              body.NotifUri.Value,
		BundledEventNotifyURI: eventSub.BundledEventNotifyUri.Value,
		MeasurementTypes:      append([]models.UpfMeasurementType(nil), upfEvent.MeasurementTypes...),
		ReportingPeriod:       body.RepPeriod.Value,
	}, nil
}

func unsupportedTopLevelField(body eventExposureCreateBody) string {
	switch {
	case body.Gpsi != nil:
		return "gpsi"
	case body.AnyUeInd != nil:
		return "anyUeInd"
	case body.GroupId != nil:
		return "groupId"
	case body.SubId != nil:
		return "subId"
	case body.AltNotifIpv4Addrs != nil:
		return "altNotifIpv4Addrs"
	case body.AltNotifIpv6Addrs != nil:
		return "altNotifIpv6Addrs"
	case body.AltNotifFqdns != nil:
		return "altNotifFqdns"
	case body.EventNotifs != nil:
		return "eventNotifs"
	case body.ImmeRep != nil:
		return "ImmeRep"
	case body.MaxReportNbr != nil:
		return "maxReportNbr"
	case body.Expiry != nil:
		return "expiry"
	case body.Guami != nil:
		return "guami"
	case body.ServiveName != nil:
		return "serviveName"
	case body.SupportedFeatures != nil:
		return "supportedFeatures"
	case body.SampRatio != nil:
		return "sampRatio"
	case body.PartitionCriteria != nil:
		return "partitionCriteria"
	case body.GrpRepTime != nil:
		return "grpRepTime"
	case body.NotifFlag != nil:
		return "notifFlag"
	default:
		return ""
	}
}

func unsupportedEventField(event eventExposureEventSubscriptionBody) string {
	switch {
	case event.DnaiChgType != nil:
		return "dnaiChgType"
	case event.DddTraDescriptors != nil:
		return "dddTraDescriptors"
	case event.DddStati != nil:
		return "dddStati"
	case event.AppIds != nil:
		return "appIds"
	case event.TargetPeriod != nil:
		return "targetPeriod"
	case event.TransacDispInd != nil:
		return "transacDispInd"
	case event.TransacMetrics != nil:
		return "transacMetrics"
	case event.UeIpAddr != nil:
		return "ueIpAddr"
	case event.BundlingAllowed != nil:
		return "bundlingAllowed"
	case event.BundleId != nil:
		return "bundleId"
	default:
		return ""
	}
}

func validEventExposureCallbackURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") &&
		u.Host != "" &&
		u.User == nil &&
		u.Fragment == ""
}

func malformedEventExposureProblem(param, reason string) *models.ProblemDetails {
	return &models.ProblemDetails{
		Title:  "Bad Request",
		Status: http.StatusBadRequest,
		InvalidParams: []models.InvalidParam{
			{
				Param:  param,
				Reason: reason,
			},
		},
	}
}
