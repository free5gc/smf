package consumer

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/free5gc/nas/nasConvert"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nchf_ConvergedCharging"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

type nchfService struct {
	consumer *Consumer

	ConvergedChargingMu sync.RWMutex

	ConvergedChargingClients map[string]*Nchf_ConvergedCharging.APIClient
}

func (s *nchfService) getConvergedChargingClient(uri string) *Nchf_ConvergedCharging.APIClient {
	if uri == "" {
		return nil
	}
	s.ConvergedChargingMu.RLock()
	client, ok := s.ConvergedChargingClients[uri]
	if ok {
		s.ConvergedChargingMu.RUnlock()
		return client
	}

	configuration := Nchf_ConvergedCharging.NewConfiguration()
	configuration.SetBasePath(uri)
	client = Nchf_ConvergedCharging.NewAPIClient(configuration)

	s.ConvergedChargingMu.RUnlock()
	s.ConvergedChargingMu.Lock()
	defer s.ConvergedChargingMu.Unlock()
	s.ConvergedChargingClients[uri] = client
	return client
}

func (s *nchfService) buildConvergedChargingRequest(smContext *smf_context.SMContext,
	multipleUnitUsage []models.MultipleUnitUsage,
) *models.ChargingDataRequest {
	var triggers []models.Trigger

	smfContext := s.consumer.Context()
	date := time.Now()

	for _, unitUsage := range multipleUnitUsage {
		for _, usedUnit := range unitUsage.UsedUnitContainer {
			triggers = append(triggers, usedUnit.Triggers...)
		}
	}

	req := &models.ChargingDataRequest{
		ChargingId:           smContext.ChargingID,
		SubscriberIdentifier: smContext.Supi,
		NfConsumerIdentification: &models.NfIdentification{
			NodeFunctionality: models.NodeFunctionality_SMF,
			NFName:            smfContext.Name,
			// not sure if NFIPv4Address is RegisterIPv4 or BindingIPv4
			NFIPv4Address: smfContext.RegisterIPv4,
		},
		InvocationTimeStamp: &date,
		Triggers:            triggers,
		PDUSessionChargingInformation: &models.PduSessionChargingInformation{
			ChargingId: smContext.ChargingID,
			UserInformation: &models.UserInformation{
				ServedGPSI: smContext.Gpsi,
				ServedPEI:  smContext.Pei,
			},
			PduSessionInformation: &models.PduSessionInformation{
				PduSessionID: smContext.PDUSessionID,
				NetworkSlicingInfo: &models.NetworkSlicingInfo{
					SNSSAI: smContext.SNssai,
				},

				PduType: nasConvert.PDUSessionTypeToModels(smContext.SelectedPDUSessionType),
				ServingNetworkFunctionID: &models.ServingNetworkFunctionId{
					ServingNetworkFunctionInformation: &models.NfIdentification{
						NodeFunctionality: models.NodeFunctionality_AMF,
					},
				},
				DnnId: smContext.Dnn,
			},
		},
		NotifyUri: fmt.Sprintf("%s://%s:%d/nsmf-callback/notify_%s",
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
	multipleUnitUsage []models.MultipleUnitUsage,
) (
	*models.ChargingDataResponse, *models.ProblemDetails, error,
) {
	logger.ChargingLog.Info("Handle SendConvergedChargingRequest")

	req := s.buildConvergedChargingRequest(smContext, multipleUnitUsage)

	var rsp models.ChargingDataResponse
	var httpResponse *http.Response
	var err error

	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NCHF_CONVERGEDCHARGING, models.NfType_CHF)
	if err != nil {
		return nil, pd, err
	}

	if smContext.SelectedCHFProfile.NfServices == nil {
		errMsg := "No CHF found"
		return nil, openapi.ProblemDetailsDataNotFound(errMsg), fmt.Errorf(errMsg)
	}

	var client *Nchf_ConvergedCharging.APIClient
	// Create Converged Charging Client for this SM Context
	for _, service := range *smContext.SelectedCHFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NCHF_CONVERGEDCHARGING {
			client = s.getConvergedChargingClient(service.ApiPrefix)
		}
	}
	if client == nil {
		errMsg := "No CONVERGEDCHARGING-CHF found"
		return nil, openapi.ProblemDetailsDataNotFound(errMsg), fmt.Errorf(errMsg)
	}

	// select the appropriate converged charging service based on trigger type
	switch requestType {
	case smf_context.CHARGING_INIT:
		rsp, httpResponse, err = client.DefaultApi.ChargingdataPost(ctx, *req)
		if httpResponse != nil {
			chargingDataRef := strings.Split(httpResponse.Header.Get("Location"), "/")
			smContext.ChargingDataRef = chargingDataRef[len(chargingDataRef)-1]
		}
	case smf_context.CHARGING_UPDATE:
		rsp, httpResponse, err = client.DefaultApi.ChargingdataChargingDataRefUpdatePost(
			ctx, smContext.ChargingDataRef, *req)
	case smf_context.CHARGING_RELEASE:
		httpResponse, err = client.DefaultApi.ChargingdataChargingDataRefReleasePost(ctx,
			smContext.ChargingDataRef, *req)
	}

	defer func() {
		if httpResponse != nil {
			if resCloseErr := httpResponse.Body.Close(); resCloseErr != nil {
				logger.ChargingLog.Errorf("RegisterNFInstance response body cannot close: %+v", resCloseErr)
			}
		}
	}()

	if err == nil {
		return &rsp, nil, nil
	} else if httpResponse != nil {
		if httpResponse.Status != err.Error() {
			return nil, nil, err
		}
		problem := err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		return nil, &problem, nil
	} else {
		return nil, nil, openapi.ReportError("server no response")
	}
}
