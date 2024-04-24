package consumer

import (
	"net/http"
	"sync"

	// "github.com/antihax/optional"
	"github.com/mohae/deepcopy"
	"github.com/pkg/errors"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/Nudm_UEContextManagement"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/util"
)

type nudmService struct {
	consumer *Consumer

	SubscriberDataManagementMu sync.RWMutex
	UEContextManagementMu      sync.RWMutex

	SubscriberDataManagementClients map[string]*Nudm_SubscriberDataManagement.APIClient
	UEContextManagementClients      map[string]*Nudm_UEContextManagement.APIClient
}

func (s *nudmService) getSubscribeDataManagementClient(uri string) *Nudm_SubscriberDataManagement.APIClient {
	if uri == "" {
		return nil
	}
	s.SubscriberDataManagementMu.RLock()
	client, ok := s.SubscriberDataManagementClients[uri]
	if ok {
		defer s.SubscriberDataManagementMu.RUnlock()
		return client
	}

	configuration := Nudm_SubscriberDataManagement.NewConfiguration()
	configuration.SetBasePath(uri)
	client = Nudm_SubscriberDataManagement.NewAPIClient(configuration)

	s.SubscriberDataManagementMu.RUnlock()
	s.SubscriberDataManagementMu.Lock()
	defer s.SubscriberDataManagementMu.Unlock()
	s.SubscriberDataManagementClients[uri] = client
	return client
}

func (s *nudmService) getUEContextManagementClient(uri string) *Nudm_UEContextManagement.APIClient {
	if uri == "" {
		return nil
	}
	s.UEContextManagementMu.RLock()
	client, ok := s.UEContextManagementClients[uri]
	if ok {
		defer s.UEContextManagementMu.RUnlock()
		return client
	}

	configuration := Nudm_UEContextManagement.NewConfiguration()
	configuration.SetBasePath(uri)
	client = Nudm_UEContextManagement.NewAPIClient(configuration)

	s.UEContextManagementMu.RUnlock()
	s.UEContextManagementMu.Lock()
	defer s.UEContextManagementMu.Unlock()
	s.UEContextManagementClients[uri] = client
	return client
}

func (s *nudmService) SendNFDiscoveryUDM() (*models.ProblemDetails, error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	smfContext := s.consumer.smf.Context()

	// Check data
	result, httpResp, localErr := s.consumer.nnrfService.NFDiscoveryUDM(ctx)

	if localErr == nil {
		smfContext.UDMProfile = result.NfInstances[0]

		for _, service := range *smfContext.UDMProfile.NfServices {
			if service.ServiceName == models.ServiceName_NUDM_SDM {
				smfContext.SubscriberDataManagementClient = s.consumer.getSubscribeDataManagementClient(service.ApiPrefix)
			}
		}

		if smfContext.SubscriberDataManagementClient == nil {
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

func (s *nudmService) SendNFDiscoveryPCF() (problemDetails *models.ProblemDetails, err error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	// Check data
	result, httpResp, localErr := s.consumer.nnrfService.NFDiscoveryPCF(ctx)

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

func (s *nudmService) SendNFDiscoveryServingAMF(smContext *smf_context.SMContext) (*models.ProblemDetails, error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	// Check data
	result, httpResp, localErr := s.consumer.nnrfService.NFDiscoveryAMF(smContext, ctx)

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

func (s *nudmService) UeCmRegistration(smCtx *smf_context.SMContext) (
	*models.ProblemDetails, error,
) {
	smfContext := s.consumer.smf.Context()

	uecmUri := util.SearchNFServiceUri(smfContext.UDMProfile, models.ServiceName_NUDM_UECM,
		models.NfServiceStatus_REGISTERED)
	if uecmUri == "" {
		return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
	}

	client := s.getUEContextManagementClient(uecmUri)

	registrationData := models.SmfRegistration{
		SmfInstanceId:               smfContext.NfInstanceID,
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

func (s *nudmService) UeCmDeregistration(smCtx *smf_context.SMContext) (*models.ProblemDetails, error) {
	smfContext := s.consumer.smf.Context()

	uecmUri := util.SearchNFServiceUri(smfContext.UDMProfile, models.ServiceName_NUDM_UECM,
		models.NfServiceStatus_REGISTERED)
	if uecmUri == "" {
		return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
	}
	client := s.getUEContextManagementClient(uecmUri)

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
