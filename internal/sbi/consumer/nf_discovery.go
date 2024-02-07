package consumer

import (
	"net/http"

	"github.com/antihax/optional"
	"github.com/mohae/deepcopy"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nnrf_NFDiscovery"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

func SendNFDiscoveryUDM() (*models.ProblemDetails, error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	// Check data
	result, httpResp, localErr := smf_context.GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_UDM, models.NfType_SMF, &localVarOptionals)

	if localErr == nil {
		smf_context.GetSelf().UDMProfile = result.NfInstances[0]

		for _, service := range *smf_context.GetSelf().UDMProfile.NfServices {
			if service.ServiceName == models.ServiceName_NUDM_SDM {
				SDMConf := Nudm_SubscriberDataManagement.NewConfiguration()
				SDMConf.SetBasePath(service.ApiPrefix)
				smf_context.GetSelf().SubscriberDataManagementClient = Nudm_SubscriberDataManagement.NewAPIClient(SDMConf)
			}
		}

		if smf_context.GetSelf().SubscriberDataManagementClient == nil {
			logger.ConsumerLog.Warnln("sdm client failed")
		}
	} else if httpResp != nil {
		defer func() {
			if resCloseErr := httpResp.Body.Close(); resCloseErr != nil {
				logger.ConsumerLog.Errorf("SearchNFInstances response body cannot close: %+v", resCloseErr)
			}
		}()
		logger.ConsumerLog.Warnln("handler returned wrong status code ", httpResp.Status)
		if httpResp.Status != localErr.Error() {
			return nil, localErr
		}
		problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		return &problem, nil
	} else {
		return nil, openapi.ReportError("server no response")
	}
	return nil, nil
}

func SendNFDiscoveryPCF() (problemDetails *models.ProblemDetails, err error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	// Set targetNfType
	targetNfType := models.NfType_PCF
	// Set requestNfType
	requesterNfType := models.NfType_SMF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	// Check data
	result, httpResp, localErr := smf_context.GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(ctx, targetNfType, requesterNfType, &localVarOptionals)

	if localErr == nil {
		logger.ConsumerLog.Traceln(result.NfInstances)
	} else if httpResp != nil {
		defer func() {
			if resCloseErr := httpResp.Body.Close(); resCloseErr != nil {
				logger.ConsumerLog.Errorf("SearchNFInstances response body cannot close: %+v", resCloseErr)
			}
		}()
		logger.ConsumerLog.Warnln("handler returned wrong status code ", httpResp.Status)
		if httpResp.Status != localErr.Error() {
			err = localErr
			return problemDetails, err
		}
		problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		problemDetails = &problem
	} else {
		err = openapi.ReportError("server no response")
	}

	return problemDetails, err
}

func SendNFDiscoveryServingAMF(smContext *smf_context.SMContext) (*models.ProblemDetails, error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	targetNfType := models.NfType_AMF
	requesterNfType := models.NfType_SMF

	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	localVarOptionals.TargetNfInstanceId = optional.NewInterface(smContext.ServingNfId)

	// Check data
	result, httpResp, localErr := smf_context.GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(ctx, targetNfType, requesterNfType, &localVarOptionals)

	if localErr == nil {
		if result.NfInstances == nil {
			if status := httpResp.StatusCode; status != http.StatusOK {
				logger.ConsumerLog.Warnln("handler returned wrong status code", status)
			}
			logger.ConsumerLog.Warnln("NfInstances is nil")
			return nil, openapi.ReportError("NfInstances is nil")
		}
		logger.ConsumerLog.Info("SendNFDiscoveryServingAMF ok")
		smContext.AMFProfile = deepcopy.Copy(result.NfInstances[0]).(models.NfProfile)
	} else if httpResp != nil {
		defer func() {
			if resCloseErr := httpResp; resCloseErr != nil {
				logger.ConsumerLog.Errorf("SearchNFInstances response body cannot close: %+v", resCloseErr)
			}
		}()
		if httpResp.Status != localErr.Error() {
			return nil, localErr
		}
		problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		return &problem, nil
	} else {
		return nil, openapi.ReportError("server no response")
	}

	return nil, nil
}
