package consumer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/antihax/optional"
	"github.com/mohae/deepcopy"
	"github.com/pkg/errors"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nnrf_NFDiscovery"
	"github.com/free5gc/openapi/Nnrf_NFManagement"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

type nnrfService struct {
	consumer *Consumer

	NFManagementgMu sync.RWMutex
	NFDiscoveryMu   sync.RWMutex

	NFManagementClients map[string]*Nnrf_NFManagement.APIClient
	NFDiscoveryClients  map[string]*Nnrf_NFDiscovery.APIClient
}

func (s *nnrfService) getNFManagementClient(uri string) *Nnrf_NFManagement.APIClient {
	if uri == "" {
		return nil
	}
	s.NFManagementgMu.RLock()
	client, ok := s.NFManagementClients[uri]
	if ok {
		s.NFManagementgMu.RUnlock()
		return client
	}

	configuration := Nnrf_NFManagement.NewConfiguration()
	configuration.SetBasePath(uri)
	client = Nnrf_NFManagement.NewAPIClient(configuration)

	s.NFManagementgMu.RUnlock()
	s.NFManagementgMu.Lock()
	defer s.NFManagementgMu.Unlock()
	s.NFManagementClients[uri] = client
	return client
}

func (s *nnrfService) getNFDiscoveryClient(uri string) *Nnrf_NFDiscovery.APIClient {
	if uri == "" {
		return nil
	}
	s.NFDiscoveryMu.RLock()
	client, ok := s.NFDiscoveryClients[uri]
	if ok {
		s.NFDiscoveryMu.RUnlock()
		return client
	}

	configuration := Nnrf_NFDiscovery.NewConfiguration()
	configuration.SetBasePath(uri)
	client = Nnrf_NFDiscovery.NewAPIClient(configuration)

	s.NFDiscoveryMu.RUnlock()
	s.NFDiscoveryMu.Lock()
	defer s.NFDiscoveryMu.Unlock()
	s.NFDiscoveryClients[uri] = client
	return client
}

func (s *nnrfService) RegisterNFInstance(ctx context.Context) error {
	smfContext := s.consumer.Context()
	client := s.getNFManagementClient(smfContext.NrfUri)
	nfProfile, err := s.buildNfProfile(smfContext)
	if err != nil {
		return errors.Wrap(err, "RegisterNFInstance buildNfProfile()")
	}

	var nf models.NfProfile
	var res *http.Response

	// Check data (Use RESTful PUT)
	for {
		nf, res, err = client.NFInstanceIDDocumentApi.
			RegisterNFInstance(ctx, smfContext.NfInstanceID, nfProfile)
		if err != nil || res == nil {
			logger.ConsumerLog.Infof("SMF register to NRF Error[%s]", err.Error())
			time.Sleep(2 * time.Second)
			continue
		}
		defer func() {
			if resCloseErr := res.Body.Close(); resCloseErr != nil {
				logger.ConsumerLog.Errorf("RegisterNFInstance response body cannot close: %+v", resCloseErr)
			}
		}()

		status := res.StatusCode
		if status == http.StatusOK {
			// NFUpdate
			break
		} else if status == http.StatusCreated {
			// NFRegister
			resourceUri := res.Header.Get("Location")
			smfContext.NfInstanceID = resourceUri[strings.LastIndex(resourceUri, "/")+1:]

			oauth2 := false
			if nf.CustomInfo != nil {
				v, ok := nf.CustomInfo["oauth2"].(bool)
				if ok {
					oauth2 = v
					logger.MainLog.Infoln("OAuth2 setting receive from NRF:", oauth2)
				}
			}
			smfContext.OAuth2Required = oauth2
			if oauth2 && smfContext.NrfCertPem == "" {
				logger.CfgLog.Error("OAuth2 enable but no nrfCertPem provided in config.")
			}
			break
		} else {
			logger.ConsumerLog.Infof("handler returned wrong status code %d", status)
		}
	}

	logger.InitLog.Infof("SMF Registration to NRF %v", nf)
	return nil
}

func (s *nnrfService) buildNfProfile(smfContext *smf_context.SMFContext) (profile models.NfProfile, err error) {
	smfProfile := smfContext.NfProfile

	sNssais := []models.Snssai{}
	for _, snssaiSmfInfo := range *smfProfile.SMFInfo.SNssaiSmfInfoList {
		sNssais = append(sNssais, *snssaiSmfInfo.SNssai)
	}

	// set nfProfile
	profile = models.NfProfile{
		NfInstanceId:  smfContext.NfInstanceID,
		NfType:        models.NfType_SMF,
		NfStatus:      models.NfStatus_REGISTERED,
		Ipv4Addresses: []string{smfContext.RegisterIPv4},
		NfServices:    smfProfile.NFServices,
		SmfInfo:       smfProfile.SMFInfo,
		SNssais:       &sNssais,
		PlmnList:      smfProfile.PLMNList,
	}
	if smfContext.Locality != "" {
		profile.Locality = smfContext.Locality
	}
	return profile, err
}

func (s *nnrfService) RetrySendNFRegistration(maxRetry int) error {
	retryCount := 0
	for retryCount < maxRetry {
		err := s.RegisterNFInstance(context.Background())
		if err == nil {
			return nil
		}
		logger.ConsumerLog.Warnf("Send NFRegistration Failed by %v", err)
		retryCount++
	}

	return fmt.Errorf("[SMF] Retry NF Registration has meet maximum")
}

func (s *nnrfService) SendDeregisterNFInstance() (problemDetails *models.ProblemDetails, err error) {
	logger.ConsumerLog.Infof("Send Deregister NFInstance")

	smfContext := s.consumer.Context()
	ctx, pd, err := smfContext.GetTokenCtx(models.ServiceName_NNRF_NFM, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	client := s.getNFManagementClient(smfContext.NrfUri)

	var res *http.Response

	res, err = client.NFInstanceIDDocumentApi.DeregisterNFInstance(ctx, smfContext.NfInstanceID)

	if err == nil {
		return problemDetails, err
	} else if res != nil {
		defer func() {
			if resCloseErr := res.Body.Close(); resCloseErr != nil {
				logger.ConsumerLog.Errorf("DeregisterNFInstance response body cannot close: %+v", resCloseErr)
			}
		}()
		if res.Status != err.Error() {
			return problemDetails, err
		}
		problem := err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		problemDetails = &problem
	} else {
		err = openapi.ReportError("server no response")
	}
	return problemDetails, err
}

func (s *nnrfService) SendSearchNFInstances(nrfUri string, targetNfType, requestNfType models.NfType,
	param *Nnrf_NFDiscovery.SearchNFInstancesParamOpts,
) (*models.SearchResult, error) {
	// Set client and set url
	smfContext := s.consumer.Context()
	client := s.getNFDiscoveryClient(smfContext.NrfUri)

	if client == nil {
		return nil, openapi.ReportError("nrf not found")
	}

	ctx, _, err := smfContext.GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return nil, err
	}

	result, res, err := client.NFInstancesStoreApi.SearchNFInstances(ctx, targetNfType, requestNfType, param)
	if err != nil {
		logger.ConsumerLog.Errorf("SearchNFInstances failed: %+v", err)
	}
	defer func() {
		if resCloseErr := res.Body.Close(); resCloseErr != nil {
			logger.ConsumerLog.Errorf("NFInstancesStoreApi response body cannot close: %+v", resCloseErr)
		}
	}()
	if res != nil && res.StatusCode == http.StatusTemporaryRedirect {
		return nil, fmt.Errorf("temporary Redirect For Non NRF Consumer")
	}
	return &result, err
}

func (s *nnrfService) NFDiscoveryUDM(ctx context.Context) (result models.SearchResult,
	httpResp *http.Response, localErr error,
) {
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	smfContext := s.consumer.Context()

	client := s.getNFDiscoveryClient(smfContext.NrfUri)
	// Check data
	result, httpResp, localErr = client.NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_UDM, models.NfType_SMF, &localVarOptionals)
	return result, httpResp, localErr
}

func (s *nnrfService) NFDiscoveryPCF(ctx context.Context) (
	result models.SearchResult, httpResp *http.Response, localErr error,
) {
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	smfContext := s.consumer.Context()

	client := s.getNFDiscoveryClient(smfContext.NrfUri)
	// Check data
	result, httpResp, localErr = client.NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_PCF, models.NfType_SMF, &localVarOptionals)
	return result, httpResp, localErr
}

func (s *nnrfService) NFDiscoveryAMF(smContext *smf_context.SMContext, ctx context.Context) (
	result models.SearchResult, httpResp *http.Response, localErr error,
) {
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	localVarOptionals.TargetNfInstanceId = optional.NewInterface(smContext.ServingNfId)

	smfContext := s.consumer.Context()

	client := s.getNFDiscoveryClient(smfContext.NrfUri)
	// Check data
	result, httpResp, localErr = client.NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_AMF, models.NfType_SMF, &localVarOptionals)
	return result, httpResp, localErr
}

func (s *nnrfService) SendNFDiscoveryUDM() (*models.ProblemDetails, error) {
	smfContext := s.consumer.Context()
	ctx, pd, err := smfContext.GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	// Check data
	result, httpResp, localErr := s.NFDiscoveryUDM(ctx)

	if localErr == nil {
		smfContext.UDMProfile = result.NfInstances[0]

		var client *Nudm_SubscriberDataManagement.APIClient
		for _, service := range *smfContext.UDMProfile.NfServices {
			if service.ServiceName == models.ServiceName_NUDM_SDM {
				client = s.consumer.nudmService.getSubscribeDataManagementClient(service.ApiPrefix)
			}
		}
		if client == nil {
			logger.ConsumerLog.Traceln("Get Subscribe Data Management Client Failed")
			return nil, fmt.Errorf("Get Subscribe Data Management Client Failed")
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

func (s *nnrfService) SendNFDiscoveryPCF() (problemDetails *models.ProblemDetails, err error) {
	ctx, pd, err := s.consumer.Context().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	// Check data
	result, httpResp, localErr := s.NFDiscoveryPCF(ctx)

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

func (s *nnrfService) SendNFDiscoveryServingAMF(smContext *smf_context.SMContext) (*models.ProblemDetails, error) {
	ctx, pd, err := s.consumer.Context().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	// Check data
	result, httpResp, localErr := s.NFDiscoveryAMF(smContext, ctx)

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

// CHFSelection will select CHF for this SM Context
func (s *nnrfService) CHFSelection(smContext *smf_context.SMContext) error {
	// Send NFDiscovery for find CHF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{
		// Supi: optional.NewString(smContext.Supi),
	}

	ctx, _, err := s.consumer.Context().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return err
	}

	client := s.getNFDiscoveryClient(s.consumer.Context().NrfUri)
	// Check data
	rsp, res, err := client.NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_CHF, models.NfType_SMF, &localVarOptionals)
	if err != nil {
		logger.ConsumerLog.Errorf("SearchNFInstances failed: %+v", err)
		return err
	}
	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.PduSessLog.Errorf("SmfEventExposureNotification response body cannot close: %+v", rspCloseErr)
		}
	}()

	if res != nil {
		if status := res.StatusCode; status != http.StatusOK {
			apiError := err.(openapi.GenericOpenAPIError)
			problemDetails := apiError.Model().(models.ProblemDetails)

			logger.CtxLog.Warningf("NFDiscovery return status: %d\n", status)
			logger.CtxLog.Warningf("Detail: %v\n", problemDetails.Title)
		}
	}

	// Select CHF from available CHF
	if len(rsp.NfInstances) > 0 {
		smContext.SelectedCHFProfile = rsp.NfInstances[0]
		return nil
	}
	return fmt.Errorf("No CHF found in CHFSelection")
}

// PCFSelection will select PCF for this SM Context
func (s *nnrfService) PCFSelection(smContext *smf_context.SMContext) error {
	ctx, _, errToken := s.consumer.Context().GetTokenCtx(models.ServiceName_NNRF_DISC, "NRF")
	if errToken != nil {
		return errToken
	}
	// Send NFDiscovery for find PCF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	if s.consumer.Context().Locality != "" {
		localVarOptionals.PreferredLocality = optional.NewString(s.consumer.Context().Locality)
	}

	client := s.getNFDiscoveryClient(s.consumer.Context().NrfUri)
	// Check data
	rsp, res, err := client.NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_PCF, models.NfType_SMF, &localVarOptionals)
	if err != nil {
		return err
	}
	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.PduSessLog.Errorf("SmfEventExposureNotification response body cannot close: %+v", rspCloseErr)
		}
	}()

	if res != nil {
		if status := res.StatusCode; status != http.StatusOK {
			apiError := err.(openapi.GenericOpenAPIError)
			problemDetails := apiError.Model().(models.ProblemDetails)

			logger.CtxLog.Warningf("NFDiscovery PCF return status: %d\n", status)
			logger.CtxLog.Warningf("Detail: %v\n", problemDetails.Title)
		}
	}

	// Select PCF from available PCF

	smContext.SelectedPCFProfile = rsp.NfInstances[0]

	return nil
}

func (s *nnrfService) SearchNFInstances(ctx context.Context, targetNfType models.NfType, requesterNfType models.NfType,
	localVarOptionals *Nnrf_NFDiscovery.SearchNFInstancesParamOpts,
) (*models.SearchResult, *http.Response, error) {
	client := s.getNFDiscoveryClient(s.consumer.Context().NrfUri)

	rsp, res, err := client.NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_CHF, models.NfType_SMF, localVarOptionals)
	if err != nil {
		return nil, nil, err
	}

	defer func() {
		if rspCloseErr := res.Body.Close(); rspCloseErr != nil {
			logger.PduSessLog.Errorf("SmfEventExposureNotification response body cannot close: %+v", rspCloseErr)
		}
	}()

	return &rsp, res, err
}
