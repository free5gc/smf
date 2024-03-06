package consumer

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/free5gc/nas/nasConvert"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

func buildConvergedChargingRequest(smContext *smf_context.SMContext,
	multipleUnitUsage []models.MultipleUnitUsage,
) *models.ChargingDataRequest {
	var triggers []models.Trigger

	smfSelf := smf_context.GetSelf()
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
			NFName:            smfSelf.Name,
			// not sure if NFIPv4Address is RegisterIPv4 or BindingIPv4
			NFIPv4Address: smfSelf.RegisterIPv4,
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
			smf_context.GetSelf().URIScheme,
			smf_context.GetSelf().RegisterIPv4,
			smf_context.GetSelf().SBIPort,
			smContext.Ref,
		),
		MultipleUnitUsage: multipleUnitUsage,
	}

	return req
}

func SendConvergedChargingRequest(smContext *smf_context.SMContext, requestType smf_context.RequestType,
	multipleUnitUsage []models.MultipleUnitUsage,
) (*models.ChargingDataResponse, *models.ProblemDetails, error) {
	logger.ChargingLog.Info("Handle SendConvergedChargingRequest")

	req := buildConvergedChargingRequest(smContext, multipleUnitUsage)

	var rsp models.ChargingDataResponse
	var httpResponse *http.Response
	var err error

	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NCHF_CONVERGEDCHARGING, models.NfType_CHF)
	if err != nil {
		return nil, pd, err
	}

	// select the appropriate converged charging service based on trigger type
	switch requestType {
	case smf_context.CHARGING_INIT:
		rsp, httpResponse, err = smContext.ChargingClient.DefaultApi.ChargingdataPost(ctx, *req)
		chargingDataRef := strings.Split(httpResponse.Header.Get("Location"), "/")
		smContext.ChargingDataRef = chargingDataRef[len(chargingDataRef)-1]
	case smf_context.CHARGING_UPDATE:
		rsp, httpResponse, err = smContext.ChargingClient.DefaultApi.ChargingdataChargingDataRefUpdatePost(
			ctx, smContext.ChargingDataRef, *req)
	case smf_context.CHARGING_RELEASE:
		httpResponse, err = smContext.ChargingClient.DefaultApi.ChargingdataChargingDataRefReleasePost(ctx,
			smContext.ChargingDataRef, *req)
	}

	defer func() {
		if resCloseErr := httpResponse.Body.Close(); resCloseErr != nil {
			logger.ChargingLog.Errorf("RegisterNFInstance response body cannot close: %+v", resCloseErr)
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
