package consumer

import (
	"github.com/pkg/errors"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nudm_UEContextManagement"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/util"
)

func UeCmRegistration(smCtx *smf_context.SMContext) (
	*models.ProblemDetails, error,
) {
	uecmUri := util.SearchNFServiceUri(smf_context.GetSelf().UDMProfile, models.ServiceName_NUDM_UECM,
		models.NfServiceStatus_REGISTERED)
	if uecmUri == "" {
		return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
	}

	configuration := Nudm_UEContextManagement.NewConfiguration()
	configuration.SetBasePath(uecmUri)
	client := Nudm_UEContextManagement.NewAPIClient(configuration)

	registrationData := models.SmfRegistration{
		SmfInstanceId:               smf_context.GetSelf().NfInstanceID,
		SupportedFeatures:           "",
		PduSessionId:                smCtx.PduSessionId,
		SingleNssai:                 smCtx.SNssai,
		Dnn:                         smCtx.Dnn,
		EmergencyServices:           false,
		PcscfRestorationCallbackUri: "",
		PlmnId:                      smCtx.Guami.PlmnId,
		PgwFqdn:                     "",
	}

	logger.PduSessLog.Infoln("UECM Registration SmfInstanceId:", registrationData.SmfInstanceId,
		" PduSessionId:", registrationData.PduSessionId, " SNssai:", registrationData.SingleNssai,
		" Dnn:", registrationData.Dnn, " PlmnId:", registrationData.PlmnId)

	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NUDM_UECM, models.NfType_UDM)
	if err != nil {
		return pd, err
	}

	_, httpResp, localErr := client.SMFRegistrationApi.SmfRegistrationsPduSessionId(ctx,
		smCtx.Supi, smCtx.PduSessionId, registrationData)
	defer func() {
		if httpResp != nil {
			if rspCloseErr := httpResp.Body.Close(); rspCloseErr != nil {
				logger.PduSessLog.Errorf("UeCmRegistration response body cannot close: %+v",
					rspCloseErr)
			}
		}
	}()

	if localErr == nil {
		smCtx.UeCmRegistered = true
		return nil, nil
	} else if httpResp != nil {
		if httpResp.Status != localErr.Error() {
			return nil, localErr
		}
		problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		return &problem, nil
	} else {
		return nil, openapi.ReportError("server no response")
	}
}

func UeCmDeregistration(smCtx *smf_context.SMContext) (*models.ProblemDetails, error) {
	uecmUri := util.SearchNFServiceUri(smf_context.GetSelf().UDMProfile, models.ServiceName_NUDM_UECM,
		models.NfServiceStatus_REGISTERED)
	if uecmUri == "" {
		return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
	}

	configuration := Nudm_UEContextManagement.NewConfiguration()
	configuration.SetBasePath(uecmUri)
	client := Nudm_UEContextManagement.NewAPIClient(configuration)

	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NUDM_UECM, models.NfType_UDM)
	if err != nil {
		return pd, err
	}

	httpResp, localErr := client.SMFDeregistrationApi.Deregistration(ctx,
		smCtx.Supi, smCtx.PduSessionId)
	defer func() {
		if httpResp != nil {
			if rspCloseErr := httpResp.Body.Close(); rspCloseErr != nil {
				logger.ConsumerLog.Errorf("UeCmDeregistration response body cannot close: %+v",
					rspCloseErr)
			}
		}
	}()
	if localErr == nil {
		smCtx.UeCmRegistered = false
		return nil, nil
	} else if httpResp != nil {
		if httpResp.Status != localErr.Error() {
			return nil, localErr
		}
		problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		return &problem, nil
	} else {
		return nil, openapi.ReportError("server no response")
	}
}
