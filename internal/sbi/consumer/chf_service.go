package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/chf/ConvCharging"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	sbi_metrics "github.com/free5gc/util/metrics/sbi"
)

type nchfService struct {
	consumer *Consumer

	ConvergedChargingMu sync.RWMutex

	ConvergedChargingClients map[string]*ConvCharging.APIClient
}

func (s *nchfService) getConvergedChargingClient(uri string) *ConvCharging.APIClient {
	if uri == "" {
		return nil
	}
	s.ConvergedChargingMu.RLock()
	client, ok := s.ConvergedChargingClients[uri]
	if ok {
		s.ConvergedChargingMu.RUnlock()
		return client
	}

	configuration := ConvCharging.NewConfiguration()
	configuration.SetBasePath(uri)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	client = ConvCharging.NewAPIClient(configuration)

	s.ConvergedChargingMu.RUnlock()
	s.ConvergedChargingMu.Lock()
	defer s.ConvergedChargingMu.Unlock()
	s.ConvergedChargingClients[uri] = client
	return client
}

func (s *nchfService) buildConvergedChargingRequest(smContext *smf_context.SMContext,
	multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage,
) *models.Chf_ConvCharging_ChargingDataRequest {
	var triggers []models.Chf_ConvCharging_Trigger

	smfContext := s.consumer.Context()
	date := time.Now()

	for _, unitUsage := range multipleUnitUsage {
		for _, usedUnit := range unitUsage.UsedUnitContainer {
			triggers = append(triggers, usedUnit.Triggers...)
		}
	}

	req := &models.Chf_ConvCharging_ChargingDataRequest{
		ChargingId:           smContext.ChargingID,
		SubscriberIdentifier: smContext.Supi,
		NfConsumerIdentification: &models.Chf_ConvCharging_NFIdentification{
			NodeFunctionality: models.Chf_ConvCharging_NodeFunctionality_SMF,
			NFName:            smfContext.NfInstanceID,
			// not sure if NFIPv4Address is RegisterIPv4 or BindingIPv4
			NFIPv4Address: smfContext.RegisterIPv4,
		},
		InvocationTimeStamp:      &date,
		InvocationSequenceNumber: smContext.NextChargingSequenceNumber(),
		Triggers:                 triggers,
		PDUSessionChargingInformation: &models.Chf_ConvCharging_PDUSessionChargingInformation{
			ChargingId: smContext.ChargingID,
			UserInformation: &models.Chf_ConvCharging_UserInformation{
				ServedGPSI: smContext.Gpsi,
				ServedPEI:  smContext.Pei,
			},
			PduSessionInformation: &models.Chf_ConvCharging_PDUSessionInformation{
				PduSessionID: smContext.PDUSessionID,
				NetworkSlicingInfo: &models.Chf_ConvCharging_NetworkSlicingInfo{
					SNSSAI: smContext.SNssai,
				},

				PduType: smf_context.PDUSessionTypeToModels(smContext.SelectedPDUSessionType),
				ServingNetworkFunctionID: &models.Chf_ConvCharging_ServingNetworkFunctionID{
					ServingNetworkFunctionInformation: &models.Chf_ConvCharging_NFIdentification{
						NodeFunctionality: models.Chf_ConvCharging_NodeFunctionality_AMF,
					},
				},
				DnnId: smContext.Dnn,
			},
		},
		NotifyUri: fmt.Sprintf("%s://%s:%d/nsmf-callback/v1/notify_%s",
			smfContext.URIScheme,
			smfContext.RegisterIPv4,
			smfContext.SBIPort,
			smContext.Ref,
		),
		MultipleUnitUsage: multipleUnitUsage,
	}

	return req
}

// prepareConvergedChargingRequest preserves unsettled update/release requests
// instead of rebuilding them with different content or a new invocation ID.
func (s *nchfService) prepareConvergedChargingRequest(
	smContext *smf_context.SMContext,
	requestType smf_context.RequestType,
	multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage,
) (*models.Chf_ConvCharging_ChargingDataRequest, string, error) {
	if requestType == smf_context.CHARGING_INIT {
		return s.buildConvergedChargingRequest(smContext, multipleUnitUsage), smContext.ChargingDataRef, nil
	}

	ref, request, err := smContext.PendingChargingRequest(requestType)
	if err != nil {
		return nil, ref, err
	}
	if request != nil {
		// Mark the outgoing copy without changing the frozen request.
		request.RetransmissionIndicator = true
		return request, ref, nil
	}
	request = s.buildConvergedChargingRequest(smContext, multipleUnitUsage)
	if err = smContext.StorePendingChargingRequest(requestType, smContext.ChargingDataRef, request); err != nil {
		return nil, "", err
	}
	ref, request, err = smContext.PendingChargingRequest(requestType)
	return request, ref, err
}

func (s *nchfService) SendConvergedChargingRequest(
	smContext *smf_context.SMContext,
	requestType smf_context.RequestType,
	multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage,
) (
	*models.Chf_ConvCharging_ChargingDataResponse, *models.ProblemDetails, error,
) {
	return s.SendConvergedChargingRequestContext(context.Background(), smContext, requestType, multipleUnitUsage)
}

// The caller's cancellation bounds retry I/O; retransmission marks only the outgoing copy.
func (s *nchfService) SendConvergedChargingRequestContext(
	parent context.Context, smContext *smf_context.SMContext, requestType smf_context.RequestType,
	multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage,
) (*models.Chf_ConvCharging_ChargingDataResponse, *models.ProblemDetails, error) {
	logger.ChargingLog.Info("Handle SendConvergedChargingRequest")

	req, chargingDataRef, err := s.prepareConvergedChargingRequest(smContext, requestType, multipleUnitUsage)
	if err != nil {
		return nil, nil, err
	}

	tokenCtx, pd, err := smf_context.GetSelf().
		GetTokenCtx(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, models.Nrf_NFMgmt_NFType_CHF)
	if err != nil {
		return nil, pd, err
	}

	var candidate string
	for _, service := range smContext.SelectedCHFProfile.NfServices {
		if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING {
			candidate = service.ApiPrefix
			break
		}
	}
	target, targetErr := smContext.ChargingTarget(requestType, candidate)
	if targetErr != nil {
		return nil, openapi.ProblemDetailsDataNotFound(targetErr.Error()), nil
	}
	client := s.getConvergedChargingClient(target)
	if client == nil {
		return nil, openapi.ProblemDetailsDataNotFound("no CHF destination"), nil
	}
	ctx := parent
	if requestType != smf_context.CHARGING_INIT {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, 5*time.Second)
		defer cancel()
	}
	if token := tokenCtx.Value(openapi.ContextOAuth2); token != nil {
		ctx = context.WithValue(ctx, openapi.ContextOAuth2, token)
	}

	var (
		response   *models.Chf_ConvCharging_ChargingDataResponse
		location   string
		requestErr error
	)
	// Keep each generated API request explicit, then handle HTTP errors once.
	switch requestType {
	case smf_context.CHARGING_INIT:
		request := &ConvCharging.ChargingdataPostRequest{RequestBody: req}
		rsp, localErr := client.DefaultApi.ChargingdataPost(ctx, request)
		requestErr = localErr
		if localErr == nil {
			response, location = rsp.Chf_ConvCharging_ChargingDataResponse, rsp.Location
		}
	case smf_context.CHARGING_UPDATE:
		request := &ConvCharging.ChargingdataChargingDataRefUpdatePostRequest{
			ChargingDataRef: &chargingDataRef,
			RequestBody:     req,
		}
		rsp, localErr := client.DefaultApi.ChargingdataChargingDataRefUpdatePost(ctx, request)
		requestErr = localErr
		if localErr == nil && rsp != nil {
			response = rsp.Chf_ConvCharging_ChargingDataResponse
		}
	case smf_context.CHARGING_RELEASE:
		request := &ConvCharging.ChargingdataChargingDataRefReleasePostRequest{
			ChargingDataRef: &chargingDataRef,
			RequestBody:     req,
		}
		_, requestErr = client.DefaultApi.ChargingdataChargingDataRefReleasePost(ctx, request)
	default:
		return nil, nil, openapi.ReportError("invalid request type")
	}

	if requestErr != nil {
		if apiErr, ok := requestErr.(openapi.GenericOpenAPIError); ok {
			return nil, chargingHTTPProblem(apiErr), nil
		}
		return nil, openapi.ProblemDetailsSystemFailure(requestErr.Error()), nil
	}

	switch requestType {
	case smf_context.CHARGING_INIT:
		parts := strings.Split(location, "/")
		smContext.ChargingDataRef = parts[len(parts)-1]
	case smf_context.CHARGING_UPDATE:
		if response == nil {
			return nil, nil, fmt.Errorf("charging update returned no response body")
		}
		if response.InvocationSequenceNumber != req.InvocationSequenceNumber {
			return nil, nil, fmt.Errorf("%w: got %d want %d", smf_context.ErrChargingResponseSequence,
				response.InvocationSequenceNumber, req.InvocationSequenceNumber)
		}
	}
	return response, nil, nil
}

// Preserve the actual HTTP status even for union response models or statuses
// without a generated ProblemDetails field. An error response is never success.
func chargingHTTPProblem(apiError openapi.GenericOpenAPIError) *models.ProblemDetails {
	var problem models.ProblemDetails
	if err := json.Unmarshal(apiError.Body(), &problem); err != nil {
		problem.Detail = apiError.Error()
	}
	problem.Status = int32(apiError.ErrorStatus)
	if problem.Cause == "" {
		problem.Cause = "CHARGING_FAILED"
	}
	if problem.Title == "" {
		problem.Title = http.StatusText(apiError.ErrorStatus)
	}
	if problem.Detail == "" {
		problem.Detail = apiError.Error()
	}
	return &problem
}
