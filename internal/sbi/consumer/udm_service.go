package consumer

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/pkg/errors"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/udm/SubscriberDataManagement"
	"github.com/free5gc/openapi/udm/UEContextManagement"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/util"
)

type nudmService struct {
	consumer *Consumer

	SubscriberDataManagementMu sync.RWMutex
	UEContextManagementMu      sync.RWMutex

	SubscriberDataManagementClients map[string]*SubscriberDataManagement.APIClient
	UEContextManagementClients      map[string]*UEContextManagement.APIClient
}

func (s *nudmService) getSubscribeDataManagementClient(uri string) *SubscriberDataManagement.APIClient {
	if uri == "" {
		return nil
	}
	s.SubscriberDataManagementMu.RLock()
	client, ok := s.SubscriberDataManagementClients[uri]
	if ok {
		s.SubscriberDataManagementMu.RUnlock()
		return client
	}

	configuration := SubscriberDataManagement.NewConfiguration()
	configuration.SetBasePath(uri)
	client = SubscriberDataManagement.NewAPIClient(configuration)

	s.SubscriberDataManagementMu.RUnlock()
	s.SubscriberDataManagementMu.Lock()
	defer s.SubscriberDataManagementMu.Unlock()
	s.SubscriberDataManagementClients[uri] = client
	return client
}

func (s *nudmService) getUEContextManagementClient(uri string) *UEContextManagement.APIClient {
	if uri == "" {
		return nil
	}
	s.UEContextManagementMu.RLock()
	client, ok := s.UEContextManagementClients[uri]
	if ok {
		s.UEContextManagementMu.RUnlock()
		return client
	}

	configuration := UEContextManagement.NewConfiguration()
	configuration.SetBasePath(uri)
	client = UEContextManagement.NewAPIClient(configuration)

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

	uecmUri := util.SearchNFServiceUri(&smfContext.UDMProfile, models.ServiceName_NUDM_UECM,
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
		PlmnId: &models.PlmnId{
			Mcc: smCtx.Guami.PlmnId.Mcc,
			Mnc: smCtx.Guami.PlmnId.Mnc,
		},
		PgwFqdn: "",
	}

	logger.PduSessLog.Infoln("UECM Registration SmfInstanceId:", registrationData.SmfInstanceId,
		" PduSessionId:", registrationData.PduSessionId, " SNssai:", registrationData.SingleNssai,
		" Dnn:", registrationData.Dnn, " PlmnId:", registrationData.PlmnId)

	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NUDM_UECM, models.NrfNfManagementNfType_UDM)
	if err != nil {
		return pd, err
	}

	request := &UEContextManagement.RegistrationRequest{
		UeId:            &smCtx.Supi,
		PduSessionId:    &smCtx.PduSessionId,
		SmfRegistration: &registrationData,
	}

	_, localErr := client.SMFSmfRegistrationApi.Registration(ctx, request)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		errorModel := err.Model().(UEContextManagement.RegistrationError)
		return &errorModel.ProblemDetails, nil

	case error:
		problemDetail := models.ProblemDetails{
			Title:  "Internal Error",
			Status: http.StatusInternalServerError,
			Detail: err.Error(),
		}
		return &problemDetail, nil

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

	uecmUri := util.SearchNFServiceUri(&smfContext.UDMProfile, models.ServiceName_NUDM_UECM,
		models.NfServiceStatus_REGISTERED)
	if uecmUri == "" {
		return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
	}
	client := s.getUEContextManagementClient(uecmUri)

	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NUDM_UECM, models.NrfNfManagementNfType_UDM)
	if err != nil {
		return pd, err
	}

	request := &UEContextManagement.SmfDeregistrationRequest{
		UeId:         &smCtx.Supi,
		PduSessionId: &smCtx.PduSessionId,
	}

	_, localErr := client.SMFDeregistrationApi.SmfDeregistration(ctx, request)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		errorModel := err.Model().(UEContextManagement.SmfDeregistrationError)
		return &errorModel.ProblemDetails, nil

	case error:
		problemDetail := models.ProblemDetails{
			Title:  "Internal Error",
			Status: http.StatusInternalServerError,
			Detail: err.Error(),
		}
		return &problemDetail, nil

	case nil:
		logger.PduSessLog.Tracef("UECM Deregistration Success")
		smCtx.UeCmRegistered = false
		return nil, nil

	default:
		return nil, openapi.ReportError("server no response")
	}
}

func (s *nudmService) GetSmData(ctx context.Context, supi string,
	request *SubscriberDataManagement.GetSmDataRequest) (
	[]models.SessionManagementSubscriptionData, error,
) {
	var client *SubscriberDataManagement.APIClient
	for _, service := range s.consumer.Context().UDMProfile.NfServices {
		if service.ServiceName == models.ServiceName_NUDM_SDM {
			SDMConf := SubscriberDataManagement.NewConfiguration()
			SDMConf.SetBasePath(service.ApiPrefix)
			client = s.getSubscribeDataManagementClient(service.ApiPrefix)
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

	sessSubData := rsp.SmSubsData.IndividualSmSubsData

	return sessSubData, err
}
