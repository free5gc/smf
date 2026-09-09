package consumer

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/udm/SDM"
	"github.com/free5gc/openapi/udm/UECM"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/util"
	sbi_metrics "github.com/free5gc/util/metrics/sbi"
)

type nudmService struct {
	consumer *Consumer

	SubscriberDataManagementMu sync.RWMutex
	UEContextManagementMu      sync.RWMutex

	SubscriberDataManagementClients map[string]*SDM.APIClient
	UEContextManagementClients      map[string]*UECM.APIClient
}

func (s *nudmService) getSubscribeDataManagementClient(uri string) *SDM.APIClient {
	if uri == "" {
		return nil
	}
	s.SubscriberDataManagementMu.RLock()
	client, ok := s.SubscriberDataManagementClients[uri]
	if ok {
		s.SubscriberDataManagementMu.RUnlock()
		return client
	}

	configuration := SDM.NewConfiguration()
	configuration.SetBasePath(uri)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	client = SDM.NewAPIClient(configuration)

	s.SubscriberDataManagementMu.RUnlock()
	s.SubscriberDataManagementMu.Lock()
	defer s.SubscriberDataManagementMu.Unlock()
	s.SubscriberDataManagementClients[uri] = client
	return client
}

func (s *nudmService) getUEContextManagementClient(uri string) *UECM.APIClient {
	if uri == "" {
		return nil
	}
	s.UEContextManagementMu.RLock()
	client, ok := s.UEContextManagementClients[uri]
	if ok {
		s.UEContextManagementMu.RUnlock()
		return client
	}

	configuration := UECM.NewConfiguration()
	configuration.SetBasePath(uri)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	client = UECM.NewAPIClient(configuration)

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

	uecmUri := util.SearchNFServiceUri(&smfContext.UDMProfile, models.Nrf_NFMgmt_ServiceName_NUDM_UECM,
		models.Nrf_NFMgmt_NFServiceStatus_REGISTERED)
	if uecmUri == "" {
		return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
	}

	client := s.getUEContextManagementClient(uecmUri)

	registrationData := models.Udm_UECM_SmfRegistration{
		SmfInstanceId:               smfContext.NfInstanceID,
		SupportedFeatures:           "",
		PduSessionId:                smCtx.PduSessionId,
		SingleNssai:                 smCtx.SNssai,
		Dnn:                         smCtx.Dnn,
		EmergencyServices:           false,
		PcscfRestorationCallbackUri: "",
		PlmnId: &models.PlmnId{
			Mcc: smCtx.Guami.PlmnId.Mcc,
			Mnc: smCtx.Guami.PlmnId.Mnc,
		},
		PgwFqdn: "",
	}

	logger.PduSessLog.Infoln("UECM Registration SmfInstanceId:", registrationData.SmfInstanceId,
		" PduSessionId:", registrationData.PduSessionId, " SNssai:", registrationData.SingleNssai,
		" Dnn:", registrationData.Dnn, " PlmnId:", registrationData.PlmnId)

	ctx, pd, err := smf_context.GetSelf().GetTokenCtxForNFInstance(
		models.Nrf_NFMgmt_ServiceName_NUDM_UECM, models.Nrf_NFMgmt_NFType_UDM, smfContext.UDMProfile.NfInstanceId)
	if err != nil {
		return pd, err
	}

	request := &UECM.RegistrationRequest{
		UeId:         &smCtx.Supi,
		PduSessionId: &smCtx.PduSessionId,
		RequestBody:  &registrationData,
	}

	_, localErr := client.SMFSmfRegistrationApi.Registration(ctx, request)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		switch errModel := err.Model().(type) {
		case UECM.RegistrationError:
			return errModel.ProblemDetails, nil
		case error:
			return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
		default:
			return nil, openapi.ReportError("openapi error")
		}
	case error:
		return openapi.ProblemDetailsSystemFailure(err.Error()), nil
	case nil:
		logger.PduSessLog.Tracef("UECM Registration Success")
		smCtx.UeCmRegistered = true
		return nil, nil
	default:
		return nil, openapi.ReportError("server no response")
	}
}

func (s *nudmService) UeCmDeregistration(smCtx *smf_context.SMContext) (*models.ProblemDetails, error) {
	smfContext := s.consumer.Context()

	uecmUri := util.SearchNFServiceUri(&smfContext.UDMProfile, models.Nrf_NFMgmt_ServiceName_NUDM_UECM,
		models.Nrf_NFMgmt_NFServiceStatus_REGISTERED)
	if uecmUri == "" {
		return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
	}
	client := s.getUEContextManagementClient(uecmUri)

	ctx, pd, err := smf_context.GetSelf().GetTokenCtxForNFInstance(
		models.Nrf_NFMgmt_ServiceName_NUDM_UECM, models.Nrf_NFMgmt_NFType_UDM, smfContext.UDMProfile.NfInstanceId)
	if err != nil {
		return pd, err
	}

	request := &UECM.SmfDeregistrationRequest{
		UeId:         &smCtx.Supi,
		PduSessionId: &smCtx.PduSessionId,
	}

	_, localErr := client.SMFDeregistrationApi.SmfDeregistration(ctx, request)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		switch errModel := err.Model().(type) {
		case UECM.SmfDeregistrationError:
			return errModel.ProblemDetails, nil
		case error:
			return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
		default:
			return nil, openapi.ReportError("openapi error")
		}
	case error:
		return openapi.ProblemDetailsSystemFailure(err.Error()), nil
	case nil:
		logger.PduSessLog.Tracef("UECM Deregistration Success")
		smCtx.UeCmRegistered = false
		return nil, nil
	default:
		return nil, openapi.ReportError("server no response")
	}
}

func (s *nudmService) GetSmData(ctx context.Context, supi string,
	request *SDM.GetSmDataRequest) (
	[]models.Udm_SDM_SessionManagementSubscriptionData, error,
) {
	var client *SDM.APIClient
	for _, service := range s.consumer.Context().UDMProfile.NfServices {
		if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NUDM_SDM {
			client = s.getSubscribeDataManagementClient(service.ApiPrefix)
			if client != nil {
				break
			}
		}
	}

	if client == nil {
		return nil, fmt.Errorf("sdm client failed")
	}

	request.Supi = &supi

	rsp, err := client.SessionManagementSubscriptionDataRetrievalApi.GetSmData(ctx, request)
	if err != nil {
		return nil, err
	}
	var sessSubData []models.Udm_SDM_SessionManagementSubscriptionData
	if rsp.Udm_SDM_SmSubsData != nil {
		sessSubData = rsp.Udm_SDM_SmSubsData.IndividualSmSubsData
	}

	return sessSubData, err
}

func (s *nudmService) Subscribe(ctx context.Context, smCtx *smf_context.SMContext, smPlmnID *models.PlmnIdNid) (
	*models.ProblemDetails, error,
) {
	var client *SDM.APIClient
	for _, service := range s.consumer.Context().UDMProfile.NfServices {
		if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NUDM_SDM {
			client = s.getSubscribeDataManagementClient(service.ApiPrefix)
			if client != nil {
				break
			}
		}
	}

	if client == nil {
		return nil, fmt.Errorf("sdm client failed")
	}

	request := &SDM.SubscribeRequest{
		UeId: &smCtx.Supi,
		RequestBody: &models.Udm_SDM_SdmSubscription{
			NfInstanceId: s.consumer.Context().NfInstanceID,
			PlmnId: &models.PlmnId{
				Mcc: smPlmnID.Mcc,
				Mnc: smPlmnID.Mnc,
			},
		},
	}

	res, localErr := client.SubscriptionCreationApi.Subscribe(ctx, request)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		switch errModel := err.Model().(type) {
		case SDM.SubscribeError:
			return errModel.ProblemDetails, nil
		case error:
			return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
		default:
			return nil, openapi.ReportError("openapi error")
		}
	case error:
		return openapi.ProblemDetailsSystemFailure(err.Error()), nil
	case nil:
		s.consumer.Context().Ues.SetSubscriptionId(smCtx.Supi, res.Udm_SDM_SdmSubscription.SubscriptionId)
		logger.PduSessLog.Infoln("SDM Subscription Successful UE:", smCtx.Supi, "SubscriptionId:",
			res.Udm_SDM_SdmSubscription.SubscriptionId)
		s.consumer.Context().Ues.IncrementPduSessionCount(smCtx.Supi)
		return nil, nil
	default:
		return nil, openapi.ReportError("server no response")
	}
}

func (s *nudmService) UnSubscribe(smCtx *smf_context.SMContext) (
	*models.ProblemDetails, error,
) {
	ctx, _, err := s.consumer.Context().GetTokenCtxForNFInstance(
		models.Nrf_NFMgmt_ServiceName_NUDM_SDM, models.Nrf_NFMgmt_NFType_UDM,
		s.consumer.Context().UDMProfile.NfInstanceId)
	if err != nil {
		return nil, err
	}

	if s.consumer.Context().Ues.IsLastPduSession(smCtx.Supi) {
		var client *SDM.APIClient
		for _, service := range s.consumer.Context().UDMProfile.NfServices {
			if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NUDM_SDM {
				client = s.getSubscribeDataManagementClient(service.ApiPrefix)
				if client != nil {
					break
				}
			}
		}

		if client == nil {
			return nil, fmt.Errorf("sdm client failed")
		}

		subscriptionId := s.consumer.Context().Ues.GetSubscriptionId(smCtx.Supi)

		request := &SDM.UnsubscribeRequest{
			UeId:           &smCtx.Supi,
			SubscriptionId: &subscriptionId,
		}

		_, localErr := client.SubscriptionDeletionApi.Unsubscribe(ctx, request)

		switch err := localErr.(type) {
		case openapi.GenericOpenAPIError:
			switch errModel := err.Model().(type) {
			case SDM.UnsubscribeError:
				return errModel.ProblemDetails, nil
			case error:
				return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
			default:
				return nil, openapi.ReportError("openapi error")
			}
		case error:
			return openapi.ProblemDetailsSystemFailure(err.Error()), nil
		case nil:
			logger.PduSessLog.Infoln("SDM UnSubscription Successful UE:", smCtx.Supi, "SubscriptionId:",
				subscriptionId)
			s.consumer.Context().Ues.DeleteUe(smCtx.Supi)
			return nil, nil
		default:
			return nil, openapi.ReportError("server no response")
		}
	} else {
		s.consumer.Context().Ues.DecrementPduSessionCount(smCtx.Supi)
	}

	return nil, nil
}
