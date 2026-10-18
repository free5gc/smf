package processor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/asaskevich/govalidator"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/validator"
)

const (
	smfEventExposureSupportedFeatures = "02000000"
	badGatewayDetail                  = "Unable to complete the downstream Nupf Event Exposure operation."
	gatewayTimeoutDetail              = "Timed out waiting for the downstream Nupf Event Exposure operation."
)

// EventExposureCreateResult contains the response and resource URI for a successful Create operation.
type EventExposureCreateResult struct {
	Response *models.Smf_EvtExpos_NsmfEventExposure
	Location string
}

// EventExposureProblem contains the HTTP status and problem details for a failed operation.
type EventExposureProblem struct {
	Status         int
	ProblemDetails *models.ProblemDetails
}

type eventExposureTarget struct {
	apiRoot   string
	supi      string
	dnn       string
	snssai    *models.Snssai
	ipAddress *models.IpAddr
}

// CreateEventExposureSubscription creates a downstream subscription and stores its local deletion state.
func (p *Processor) CreateEventExposureSubscription(
	ctx context.Context,
	request *models.Smf_EvtExpos_NsmfEventExposure,
	pduSessionIDPresent bool,
) (*EventExposureCreateResult, *EventExposureProblem) {
	negotiatedFeatures, problem := validateEventExposureRequest(request, pduSessionIDPresent)
	if problem != nil {
		return nil, problem
	}

	target, problem := p.resolveEventExposureTarget(request)
	if problem != nil {
		return nil, problem
	}

	downstreamRequest := buildNupfEventExposureRequest(p.Context().NfInstanceID, request, target)
	downstreamID, err := p.Consumer().CreateNupfEventSubscription(ctx, target.apiRoot, downstreamRequest)
	if err != nil {
		return nil, nupfEventExposureProblem(err)
	}

	localID := smf_context.StoreEventExposureSubscription(target.apiRoot, downstreamID)
	return &EventExposureCreateResult{
		Response: eventExposureResponse(request, localID, negotiatedFeatures),
		Location: fmt.Sprintf("%s://%s:%d%s/subscriptions/%s",
			p.Context().URIScheme,
			p.Context().RegisterIPv4,
			p.Context().SBIPort,
			factory.SmfEventExposureResUriPrefix,
			localID,
		),
	}, nil
}

// DeleteEventExposureSubscription deletes a downstream subscription and removes its local state after success.
func (p *Processor) DeleteEventExposureSubscription(
	ctx context.Context,
	localID string,
) *EventExposureProblem {
	record, ok := smf_context.GetEventExposureSubscription(localID)
	if !ok {
		return eventExposureNotFoundProblem()
	}

	record.LockDelete()
	defer record.UnlockDelete()
	if !smf_context.IsCurrentEventExposureSubscription(localID, record) {
		return eventExposureNotFoundProblem()
	}

	apiRoot, downstreamID := record.Coordinates()
	if err := p.Consumer().DeleteNupfEventSubscription(ctx, apiRoot, downstreamID); err != nil {
		return nupfEventExposureProblem(err)
	}

	smf_context.RemoveEventExposureSubscription(localID, record)
	return nil
}

func validateEventExposureRequest(
	request *models.Smf_EvtExpos_NsmfEventExposure,
	pduSessionIDPresent bool,
) (string, *EventExposureProblem) {
	if request == nil || !pduSessionIDPresent || request.PduSeId < 0 || request.PduSeId > 255 ||
		!validator.IsValidSupi(request.Supi) || !govalidator.IsUUIDv4(request.NfId) ||
		request.NotifId == "" || !validEventExposureCallbackURI(request.NotifUri) ||
		hasOutOfProfileTopLevelField(request) {
		return "", eventExposureBadRequestProblem()
	}

	if len(request.EventSubs) != 1 || request.EventSubs[0].Event != models.Smf_EvtExpos_SmfEvent_UPF_EVENT ||
		hasOutOfProfileEventSubscriptionField(&request.EventSubs[0]) || len(request.EventSubs[0].UpfEvents) != 1 {
		return "", eventExposureBadRequestProblem()
	}

	upfEvent := &request.EventSubs[0].UpfEvents[0]
	if hasOutOfProfileUpfEventField(upfEvent) ||
		upfEvent.Type != models.Upf_EvtExpos_EventType_USER_DATA_USAGE_MEASURES ||
		upfEvent.GranularityOfMeasurement != models.Upf_EvtExpos_GranularityOfMeasurement_SESSION ||
		len(upfEvent.MeasurementTypes) == 0 {
		return "", eventExposureBadRequestProblem()
	}
	for _, measurementType := range upfEvent.MeasurementTypes {
		if measurementType != models.Upf_EvtExpos_MeasurementType_VOLUME_MEASUREMENT &&
			measurementType != models.Upf_EvtExpos_MeasurementType_THROUGHPUT_MEASUREMENT {
			return "", eventExposureBadRequestProblem()
		}
	}

	if request.NotifMethod != models.Smf_EvtExpos_NotificationMethod_PERIODIC || request.RepPeriod <= 0 {
		return "", eventExposureBadRequestProblem()
	}

	consumerFeatures, err := openapi.NewSupportedFeature(request.SupportedFeatures)
	if err != nil || !consumerFeatures.GetFeature(26) {
		return "", eventExposureBadRequestProblem()
	}
	smfFeatures, err := openapi.NewSupportedFeature(smfEventExposureSupportedFeatures)
	if err != nil {
		return "", eventExposureBadRequestProblem()
	}
	return smfFeatures.NegotiateWith(consumerFeatures).String(), nil
}

func validEventExposureCallbackURI(rawURI string) bool {
	parsed, err := url.Parse(rawURI)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Hostname() != "" && parsed.User == nil && parsed.RawQuery == "" &&
		!parsed.ForceQuery && parsed.Fragment == ""
}

func hasOutOfProfileTopLevelField(request *models.Smf_EvtExpos_NsmfEventExposure) bool {
	return request.Gpsi != "" || request.AnyUeInd || request.GroupId != "" || request.Dnai != "" ||
		request.SsId != "" || request.BssId != "" || request.UpfId != "" || request.SubId != "" ||
		len(request.AltNotifIpv4Addrs) != 0 || len(request.AltNotifIpv6Addrs) != 0 ||
		len(request.AltNotifFqdns) != 0 || len(request.EventNotifs) != 0 || request.ImmeRep ||
		request.MaxReportNbr != 0 || request.Expiry != nil || request.Guami != nil || request.ServiveName != "" ||
		request.SampRatio != 0 || len(request.PartitionCriteria) != 0 || request.GrpRepTime != 0 ||
		request.NotifFlag != "" || request.NotifFlagInstruct != nil || request.MutingSetting != nil ||
		request.DefQosSupp || request.QosMonPending
}

func hasOutOfProfileEventSubscriptionField(subscription *models.Smf_EvtExpos_EventSubscription) bool {
	return subscription.DnaiChgType != "" || len(subscription.DddTraDescriptors) != 0 ||
		len(subscription.DddStati) != 0 || len(subscription.AppIds) != 0 || subscription.NetworkArea != nil ||
		subscription.TargetPeriod != nil || subscription.TransacDispInd || len(subscription.TransacMetrics) != 0 ||
		subscription.UeIpAddr != nil
}

func hasOutOfProfileUpfEventField(upfEvent *models.Upf_EvtExpos_UpfEvent) bool {
	return upfEvent.ImmediateFlag || len(upfEvent.AppIds) != 0 || len(upfEvent.TrafficFilters) != 0 ||
		upfEvent.ReportingSuggestionInfo != nil
}

func (p *Processor) resolveEventExposureTarget(
	request *models.Smf_EvtExpos_NsmfEventExposure,
) (*eventExposureTarget, *EventExposureProblem) {
	smContext := smf_context.GetSMContextById(request.Supi, request.PduSeId)
	if smContext == nil {
		return nil, eventExposureNotFoundProblem()
	}

	smContext.SMLock.Lock()
	if smContext.State() != smf_context.Active || smContext.PDUAddress == nil || smContext.SelectedUPF == nil ||
		(request.Dnn != "" && (smContext.Dnn == "" || request.Dnn != smContext.Dnn)) ||
		(request.Snssai != nil && !sessionSnssaiMatches(smContext.SNssai, request.Snssai)) {
		smContext.SMLock.Unlock()
		return nil, eventExposureNotFoundProblem()
	}

	target := &eventExposureTarget{
		supi:      smContext.Supi,
		dnn:       smContext.Dnn,
		snssai:    cloneSnssai(smContext.SNssai),
		ipAddress: eventExposureIPAddress(smContext.PDUAddress),
	}
	selectedUPF := smContext.SelectedUPF
	smContext.SMLock.Unlock()
	if target.ipAddress == nil {
		return nil, eventExposureNotFoundProblem()
	}

	userPlaneInformation := p.Context().UserPlaneInformation
	if userPlaneInformation == nil {
		return nil, eventExposureNotFoundProblem()
	}
	userPlaneInformation.Mu.RLock()
	target.apiRoot = selectedUPF.NupfEeApiRoot
	userPlaneInformation.Mu.RUnlock()
	if target.apiRoot == "" {
		return nil, eventExposureServiceUnavailableProblem()
	}
	return target, nil
}

func sessionSnssaiMatches(session, selector *models.Snssai) bool {
	if session == nil {
		return false
	}
	return (&smf_context.SNssai{Sst: session.Sst, Sd: session.Sd}).EqualModelsSnssai(selector)
}

func eventExposureIPAddress(ipAddress net.IP) *models.IpAddr {
	if ipv4 := ipAddress.To4(); ipv4 != nil {
		return &models.IpAddr{Ipv4Addr: ipv4.String()}
	}
	if ipv6 := ipAddress.To16(); ipv6 != nil {
		return &models.IpAddr{Ipv6Addr: ipv6.String()}
	}
	return nil
}

func buildNupfEventExposureRequest(
	nfInstanceID string,
	request *models.Smf_EvtExpos_NsmfEventExposure,
	target *eventExposureTarget,
) *models.Upf_EvtExpos_CreateEventSubscription {
	requestedEvent := request.EventSubs[0].UpfEvents[0]
	return &models.Upf_EvtExpos_CreateEventSubscription{
		Subscription: &models.Upf_EvtExpos_UpfEventSubscription{
			EventList: []models.Upf_EvtExpos_UpfEvent{
				{
					Type:                     models.Upf_EvtExpos_EventType_USER_DATA_USAGE_MEASURES,
					MeasurementTypes:         append([]models.Upf_EvtExpos_MeasurementType(nil), requestedEvent.MeasurementTypes...),
					GranularityOfMeasurement: models.Upf_EvtExpos_GranularityOfMeasurement_SESSION,
				},
			},
			EventNotifyUri:      request.NotifUri,
			NotifyCorrelationId: request.NotifId,
			EventReportingMode: &models.Upf_EvtExpos_UpfEventMode{
				Trigger:   models.Upf_EvtExpos_UpfEventTrigger_PERIODIC,
				RepPeriod: request.RepPeriod,
			},
			NfId:        nfInstanceID,
			UeIpAddress: target.ipAddress,
			Supi:        target.supi,
			Dnn:         target.dnn,
			Snssai:      cloneSnssai(target.snssai),
		},
	}
}

func eventExposureResponse(
	request *models.Smf_EvtExpos_NsmfEventExposure,
	localID string,
	negotiatedFeatures string,
) *models.Smf_EvtExpos_NsmfEventExposure {
	requestedEvent := request.EventSubs[0].UpfEvents[0]
	return &models.Smf_EvtExpos_NsmfEventExposure{
		Supi:              request.Supi,
		PduSeId:           request.PduSeId,
		Dnn:               request.Dnn,
		Snssai:            cloneSnssai(request.Snssai),
		NfId:              request.NfId,
		SubId:             localID,
		NotifId:           request.NotifId,
		NotifUri:          request.NotifUri,
		NotifMethod:       models.Smf_EvtExpos_NotificationMethod_PERIODIC,
		RepPeriod:         request.RepPeriod,
		SupportedFeatures: negotiatedFeatures,
		EventSubs: []models.Smf_EvtExpos_EventSubscription{
			{
				Event: models.Smf_EvtExpos_SmfEvent_UPF_EVENT,
				UpfEvents: []models.Upf_EvtExpos_UpfEvent{
					{
						Type:                     models.Upf_EvtExpos_EventType_USER_DATA_USAGE_MEASURES,
						MeasurementTypes:         append([]models.Upf_EvtExpos_MeasurementType(nil), requestedEvent.MeasurementTypes...),
						GranularityOfMeasurement: models.Upf_EvtExpos_GranularityOfMeasurement_SESSION,
					},
				},
			},
		},
	}
}

func cloneSnssai(snssai *models.Snssai) *models.Snssai {
	if snssai == nil {
		return nil
	}
	return &models.Snssai{Sst: snssai.Sst, Sd: snssai.Sd}
}

func nupfEventExposureProblem(err error) *EventExposureProblem {
	var downstreamError *consumer.NupfEventExposureError
	if errors.As(err, &downstreamError) {
		if downstreamError.TimedOut && downstreamError.StatusCode == 0 {
			return &EventExposureProblem{
				Status: http.StatusGatewayTimeout,
				ProblemDetails: &models.ProblemDetails{
					Title:  "Gateway Timeout",
					Status: http.StatusGatewayTimeout,
					Detail: gatewayTimeoutDetail,
				},
			}
		}
		cause := ""
		if downstreamError.StatusCode == http.StatusTooManyRequests ||
			(downstreamError.StatusCode >= 500 && downstreamError.StatusCode <= 599) {
			cause = "INBOUND_SERVER_ERROR"
		}
		return eventExposureBadGatewayProblem(cause)
	}
	return eventExposureBadGatewayProblem("")
}

func eventExposureBadRequestProblem() *EventExposureProblem {
	return &EventExposureProblem{
		Status: http.StatusBadRequest,
		ProblemDetails: &models.ProblemDetails{
			Title:  "Malformed request syntax",
			Status: http.StatusBadRequest,
			Detail: "The Event Exposure request is invalid.",
		},
	}
}

func eventExposureNotFoundProblem() *EventExposureProblem {
	return &EventExposureProblem{
		Status: http.StatusNotFound,
		ProblemDetails: &models.ProblemDetails{
			Title:  "Data not found",
			Status: http.StatusNotFound,
			Detail: "The requested Event Exposure resource was not found.",
		},
	}
}

func eventExposureServiceUnavailableProblem() *EventExposureProblem {
	return &EventExposureProblem{
		Status: http.StatusServiceUnavailable,
		ProblemDetails: &models.ProblemDetails{
			Title:  "Service Unavailable",
			Status: http.StatusServiceUnavailable,
			Detail: "The selected UPF does not expose Nupf Event Exposure.",
		},
	}
}

func eventExposureBadGatewayProblem(cause string) *EventExposureProblem {
	return &EventExposureProblem{
		Status: http.StatusBadGateway,
		ProblemDetails: &models.ProblemDetails{
			Title:  "Bad Gateway",
			Status: http.StatusBadGateway,
			Detail: badGatewayDetail,
			Cause:  cause,
		},
	}
}
