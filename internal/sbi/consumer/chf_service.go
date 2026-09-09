package consumer

import (
	"fmt"
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
			NFName:            smfContext.Name,
			// not sure if NFIPv4Address is RegisterIPv4 or BindingIPv4
			NFIPv4Address: smfContext.RegisterIPv4,
		},
		InvocationTimeStamp: &date,
		Triggers:            triggers,
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

func (s *nchfService) SendConvergedChargingRequest(
	smContext *smf_context.SMContext,
	requestType smf_context.RequestType,
	multipleUnitUsage []models.Chf_ConvCharging_MultipleUnitUsage,
) (
	*models.Chf_ConvCharging_ChargingDataResponse, *models.ProblemDetails, error,
) {
	logger.ChargingLog.Info("Handle SendConvergedChargingRequest")

	req := s.buildConvergedChargingRequest(smContext, multipleUnitUsage)

	ctx, pd, err := smf_context.GetSelf().
		GetTokenCtxForNFInstance(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING,
			models.Nrf_NFMgmt_NFType_CHF, smContext.SelectedCHFProfile.NfInstanceId)
	if err != nil {
		return nil, pd, err
	}

	if smContext.SelectedCHFProfile.NfServices == nil {
		errMsg := "no CHF found"
		return nil, openapi.ProblemDetailsDataNotFound(errMsg), fmt.Errorf("%s", errMsg)
	}

	var client *ConvCharging.APIClient
	// Create Converged Charging Client for this SM Context
	for _, service := range smContext.SelectedCHFProfile.NfServices {
		if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING {
			client = s.getConvergedChargingClient(service.ApiPrefix)
		}
	}
	if client == nil {
		errMsg := "no CONVERGEDCHARGING-CHF found"
		return nil, openapi.ProblemDetailsDataNotFound(errMsg), fmt.Errorf("%s", errMsg)
	}

	// select the appropriate converged charging service based on trigger type
	switch requestType {
	case smf_context.CHARGING_INIT:
		postChargingDataRequest := &ConvCharging.ChargingdataPostRequest{
			RequestBody: req,
		}
		rspPost, localErr := client.DefaultApi.ChargingdataPost(ctx, postChargingDataRequest)

		switch err := localErr.(type) {
		case openapi.GenericOpenAPIError:
			switch errModel := err.Model().(type) {
			case ConvCharging.ChargingdataPostError:
				return nil, errModel.ProblemDetails, nil
			case error:
				return nil, openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
			default:
				return nil, nil, openapi.ReportError("openapi error")
			}
		case error:
			return nil, openapi.ProblemDetailsSystemFailure(err.Error()), nil
		case nil:
			chargingDataRef := strings.Split(rspPost.Location, "/")
			smContext.ChargingDataRef = chargingDataRef[len(chargingDataRef)-1]
			return rspPost.Chf_ConvCharging_ChargingDataResponse, nil, nil
		default:
			return nil, nil, openapi.ReportError("server no response")
		}
	case smf_context.CHARGING_UPDATE:
		updateChargingDataRequest := &ConvCharging.ChargingdataChargingDataRefUpdatePostRequest{
			ChargingDataRef: &smContext.ChargingDataRef,
			RequestBody:     req,
		}
		rspUpdate, localErr := client.DefaultApi.ChargingdataChargingDataRefUpdatePost(ctx, updateChargingDataRequest)

		switch err := localErr.(type) {
		case openapi.GenericOpenAPIError:
			switch errModel := err.Model().(type) {
			case ConvCharging.ChargingdataChargingDataRefUpdatePostError:
				return nil, errModel.ProblemDetails, nil
			case error:
				return nil, openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
			default:
				return nil, nil, openapi.ReportError("openapi error")
			}
		case error:
			return nil, openapi.ProblemDetailsSystemFailure(err.Error()), nil
		case nil:
			return rspUpdate.Chf_ConvCharging_ChargingDataResponse, nil, nil
		default:
			return nil, nil, openapi.ReportError("server no response")
		}
	case smf_context.CHARGING_RELEASE:
		releaseChargingDataRequest := &ConvCharging.ChargingdataChargingDataRefReleasePostRequest{
			ChargingDataRef: &smContext.ChargingDataRef,
			RequestBody:     req,
		}
		_, localErr := client.DefaultApi.ChargingdataChargingDataRefReleasePost(ctx, releaseChargingDataRequest)

		switch err := localErr.(type) {
		case openapi.GenericOpenAPIError:
			switch errModel := err.Model().(type) {
			case ConvCharging.ChargingdataChargingDataRefReleasePostError:
				return nil, errModel.ProblemDetails, nil
			case error:
				return nil, openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
			default:
				return nil, nil, openapi.ReportError("openapi error")
			}
		case error:
			return nil, openapi.ProblemDetailsSystemFailure(err.Error()), nil
		case nil:
			return nil, nil, nil
		default:
			return nil, nil, openapi.ReportError("server no response")
		}
	default:
		return nil, nil, openapi.ReportError("invalid request type")
	}
}
