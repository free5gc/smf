package consumer

import (
	"context"
	"fmt"
	"net/http"
	"sync"

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
		s.SubscriberDataManagementMu.RUnlock()
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
		s.UEContextManagementMu.RUnlock()
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

func (s *nudmService) UeCmRegistration(smCtx *smf_context.SMContext) (
	*models.ProblemDetails, error,
) {
	smfContext := s.consumer.Context()

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
	smfContext := s.consumer.Context()

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

func (s *nudmService) GetSmData(ctx context.Context, supi string,
	localVarOptionals *Nudm_SubscriberDataManagement.GetSmDataParamOpts) (
	[]models.SessionManagementSubscriptionData, *http.Response, error,
) {
	var client *Nudm_SubscriberDataManagement.APIClient
	for _, service := range *s.consumer.Context().UDMProfile.NfServices {
		if service.ServiceName == models.ServiceName_NUDM_SDM {
			SDMConf := Nudm_SubscriberDataManagement.NewConfiguration()
			SDMConf.SetBasePath(service.ApiPrefix)
			client = s.getSubscribeDataManagementClient(service.ApiPrefix)
		}
	}

	if client == nil {
		return nil, nil, fmt.Errorf("sdm client failed")
	}

	sessSubData, rsp, err := client.SessionManagementSubscriptionDataRetrievalApi.GetSmData(ctx, supi, localVarOptionals)
	if err != nil {
		return nil, nil, err
	}

	defer func() {
		if rspCloseErr := rsp.Body.Close(); rspCloseErr != nil {
			logger.ConsumerLog.Errorf("GetSmData response body cannot close: %+v", rspCloseErr)
		}
	}()

	return sessSubData, rsp, err
}
