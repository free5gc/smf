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
		defer s.NFManagementgMu.RUnlock()
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
		defer s.NFDiscoveryMu.RUnlock()
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

// Done 4/28 14:03
func (s *nnrfService) RegisterNFInstance() error {
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
			RegisterNFInstance(context.TODO(), smfContext.NfInstanceID, nfProfile)
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
			// resouceNrfUri := resourceUri[strings.LastIndex(resourceUri, "/"):]
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
			// fmt.Errorf("NRF return wrong status code %d", status)
		}
	}

	logger.InitLog.Infof("SMF Registration to NRF %v", nf)
	return nil
}

// Done 4/28 14:03
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

// Done 4/28 14:04
func (s *nnrfService) RetrySendNFRegistration(MaxRetry int) error {
	retryCount := 0
	for retryCount < MaxRetry {
		err := s.RegisterNFInstance()
		if err == nil {
			return nil
		}
		logger.ConsumerLog.Warnf("Send NFRegistration Failed by %v", err)
		retryCount++
	}

	return fmt.Errorf("[SMF] Retry NF Registration has meet maximum")
}

// func (s *nnrfService) SendNFDeregistration() error {
// 	// Check data (Use RESTful DELETE)

// 	ctx, _, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_NFM, models.NfType_NRF)
// 	if err != nil {
// 		return err
// 	}

// 	res, localErr := smf_context.GetSelf().
// 		NFManagementClient.
// 		NFInstanceIDDocumentApi.
// 		DeregisterNFInstance(ctx, smf_context.GetSelf().NfInstanceID)
// 	if localErr != nil {
// 		logger.ConsumerLog.Warnln(localErr)
// 		return localErr
// 	}
// 	defer func() {
// 		if resCloseErr := res.Body.Close(); resCloseErr != nil {
// 			logger.ConsumerLog.Errorf("DeregisterNFInstance response body cannot close: %+v", resCloseErr)
// 		}
// 	}()
// 	if res != nil {
// 		if status := res.StatusCode; status != http.StatusNoContent {
// 			logger.ConsumerLog.Warnln("handler returned wrong status code ", status)
// 			return openapi.ReportError("handler returned wrong status code %d", status)
// 		}
// 	}
// 	return nil
// }

// Done 4/26 18:44
func (s *nnrfService) SendDeregisterNFInstance() (problemDetails *models.ProblemDetails, err error) {
	logger.ConsumerLog.Infof("Send Deregister NFInstance")

	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_NFM, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	smfContext := s.consumer.Context()
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

// Done 4/28 14:18
func (s *nnrfService) SendSearchNFInstances(nrfUri string, targetNfType, requestNfType models.NfType,
	param *Nnrf_NFDiscovery.SearchNFInstancesParamOpts,
) (*models.SearchResult, error) {
	// Set client and set url
	smfContext := s.consumer.Context()
	client := s.getNFDiscoveryClient(smfContext.NrfUri)

	if client == nil {
		return nil, openapi.ReportError("nrf not found")
	}

	ctx, _, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
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
	// Not sure (?
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
	// Not sure (?
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
	// Not sure (?
	client := s.getNFDiscoveryClient(smfContext.NrfUri)
	// Check data
	result, httpResp, localErr = client.NFInstancesStoreApi.
		SearchNFInstances(ctx, models.NfType_AMF, models.NfType_SMF, &localVarOptionals)
	return result, httpResp, localErr
}

func (s *nnrfService) SendNFDiscoveryUDM() (*models.ProblemDetails, error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
	if err != nil {
		return pd, err
	}

	smfContext := s.consumer.Context()

	// Check data
	result, httpResp, localErr := s.NFDiscoveryUDM(ctx)

	if localErr == nil {
		smfContext.UDMProfile = result.NfInstances[0]

		for _, service := range *smfContext.UDMProfile.NfServices {
			if service.ServiceName == models.ServiceName_NUDM_SDM {
				smfContext.SubscriberDataManagementClient = s.consumer.nudmService.
					getSubscribeDataManagementClient(service.ApiPrefix)
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

func (s *nnrfService) SendNFDiscoveryPCF() (problemDetails *models.ProblemDetails, err error) {
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
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
	ctx, pd, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NNRF_DISC, models.NfType_NRF)
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
