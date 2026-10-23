package consumer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/nrf/NFDisc"
	"github.com/free5gc/openapi/nrf/NFMgmt"
	"github.com/free5gc/openapi/udm/SDM"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	sbi_metrics "github.com/free5gc/util/metrics/sbi"
)

type nnrfService struct {
	consumer *Consumer

	NFManagementgMu sync.RWMutex
	NFDiscoveryMu   sync.RWMutex

	NFManagementClients map[string]*NFMgmt.APIClient
	NFDiscoveryClients  map[string]*NFDisc.APIClient
}

func (s *nnrfService) getNFManagementClient(uri string) *NFMgmt.APIClient {
	if uri == "" {
		return nil
	}
	s.NFManagementgMu.RLock()
	client, ok := s.NFManagementClients[uri]
	if ok {
		s.NFManagementgMu.RUnlock()
		return client
	}

	configuration := NFMgmt.NewConfiguration()
	configuration.SetBasePath(uri)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	client = NFMgmt.NewAPIClient(configuration)

	s.NFManagementgMu.RUnlock()
	s.NFManagementgMu.Lock()
	defer s.NFManagementgMu.Unlock()
	s.NFManagementClients[uri] = client
	return client
}

func (s *nnrfService) getNFDiscoveryClient(uri string) *NFDisc.APIClient {
	if uri == "" {
		return nil
	}
	s.NFDiscoveryMu.RLock()
	client, ok := s.NFDiscoveryClients[uri]
	if ok {
		s.NFDiscoveryMu.RUnlock()
		return client
	}

	configuration := NFDisc.NewConfiguration()
	configuration.SetBasePath(uri)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	client = NFDisc.NewAPIClient(configuration)

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

	var nf models.Nrf_NFMgmt_NFProfile
	var res *NFMgmt.RegisterNFInstanceResponse
	registerNFInstanceRequest := &NFMgmt.RegisterNFInstanceRequest{
		NfInstanceID: &smfContext.NfInstanceID,
		RequestBody:  &nfProfile,
	}

	// Check data (Use RESTful PUT)
	finish := false
	for !finish {
		select {
		case <-ctx.Done():
			return fmt.Errorf("RegisterNFInstance context done")
		default:
			res, err = client.NFInstanceIDDocumentApi.RegisterNFInstance(ctx, registerNFInstanceRequest)
			if err != nil || res == nil {
				logger.ConsumerLog.Errorf("SMF register to NRF Error[%s]", err.Error())
				time.Sleep(2 * time.Second)
				continue
			}
			if res.Nrf_NFMgmt_NFProfile != nil {
				nf = *res.Nrf_NFMgmt_NFProfile
			}

			// http.StatusOK
			if res.Location == "" {
				// NFUpdate
				finish = true
			} else { // http.StatusCreated
				// NFRegister
				resourceUri := res.Location
				smfContext.NfInstanceID = resourceUri[strings.LastIndex(resourceUri, "/")+1:]

				oauth2 := false
				if customInfo, ok := nf.CustomInfo.(map[string]interface{}); ok {
					if v, isBool := customInfo["oauth2"].(bool); isBool {
						oauth2 = v
						logger.MainLog.Infoln("OAuth2 setting receive from NRF:", oauth2)
					}
				}
				if oauthErr := smfContext.SetOAuth2Required(oauth2); oauthErr != nil {
					return oauthErr
				}
				finish = true
			}
		}
	}

	logger.InitLog.Infof("SMF Registration to NRF %v", nf)
	return nil
}

func (s *nnrfService) buildNfProfile(smfContext *smf_context.SMFContext) (
	profile models.Nrf_NFMgmt_NFProfile, err error,
) {
	smfProfile := smfContext.NfProfile
	if smfProfile.NFServices == nil {
		return profile, fmt.Errorf("SMF NFServices is nil")
	}

	services := make([]models.Nrf_NFMgmt_NFService, 0, len(*smfProfile.NFServices))
	for _, nfService := range *smfProfile.NFServices {
		allowed, known := smf_context.AllowedNfTypesForService(nfService.ServiceName)
		if !known {
			return profile, fmt.Errorf("no AllowedNfTypes policy for service %q", nfService.ServiceName)
		}
		nfService.AllowedNfTypes = allowed
		services = append(services, nfService)
	}

	sNssais := []models.ExtSnssai{}
	for _, snssaiSmfInfo := range smfProfile.SMFInfo.SNssaiSmfInfoList {
		sNssais = append(sNssais, *snssaiSmfInfo.SNssai)
	}

	// set nfProfile
	profile = models.Nrf_NFMgmt_NFProfile{
		NfInstanceId:  smfContext.NfInstanceID,
		NfType:        models.Nrf_NFMgmt_NFType_SMF,
		NfStatus:      models.Nrf_NFMgmt_NFStatus_REGISTERED,
		Ipv4Addresses: []string{smfContext.RegisterIPv4},
		NfServices:    services,
		SmfInfo:       smfProfile.SMFInfo,
		SNssais:       sNssais,
	}
	// plmnList is optional in the config, only set it when it is configured
	if smfProfile.PLMNList != nil {
		profile.PlmnList = *smfProfile.PLMNList
	}
	if smfContext.Locality != "" {
		profile.Locality = smfContext.Locality
	}
	return profile, err
}

func (s *nnrfService) SendDeregisterNFInstance() (err error) {
	logger.ConsumerLog.Infof("Send Deregister NFInstance")

	smfContext := s.consumer.Context()
	ctx, pd, err := smfContext.GetTokenCtxForNRF(models.Nrf_NFMgmt_ServiceName_NNRF_NFM)
	if err != nil {
		logger.ConsumerLog.Errorf("Get token context failed, problem details: %+v", pd)
		return err
	}

	client := s.getNFManagementClient(smfContext.NrfUri)
	request := &NFMgmt.DeregisterNFInstanceRequest{
		NfInstanceID: &smfContext.NfInstanceID,
	}

	_, err = client.NFInstanceIDDocumentApi.DeregisterNFInstance(ctx, request)

	return err
}

func (s *nnrfService) SendSearchNFInstances(
	nrfUri string,
	targetNfType, requestNfType models.Nrf_NFMgmt_NFType,
	param *NFDisc.SearchNFInstancesRequest,
) (*models.Nrf_NFDisc_SearchResult, error) {
	// Set client and set url
	smfContext := s.consumer.Context()
	client := s.getNFDiscoveryClient(smfContext.NrfUri)

	if client == nil {
		return nil, openapi.ReportError("nrf not found")
	}

	ctx, _, err := smfContext.GetTokenCtxForNRF(models.Nrf_NFMgmt_ServiceName_NNRF_DISC)
	if err != nil {
		return nil, err
	}

	res, err := client.NFInstancesStoreApi.SearchNFInstances(ctx, param)
	if err != nil || res == nil {
		logger.ConsumerLog.Errorf("SearchNFInstances failed: %+v", err)
		return nil, err
	}
	return res.Nrf_NFDisc_SearchResult, err
}

func (s *nnrfService) NFDiscoveryUDM(ctx context.Context) (
	result models.Nrf_NFDisc_SearchResult, localErr error,
) {
	targetNfType := models.Nrf_NFMgmt_NFType_UDM
	requesterNfType := models.Nrf_NFMgmt_NFType_SMF
	request := &NFDisc.SearchNFInstancesRequest{
		TargetNfType:    &targetNfType,
		RequesterNfType: &requesterNfType,
	}

	smfContext := s.consumer.Context()

	client := s.getNFDiscoveryClient(smfContext.NrfUri)
	// Check data
	res, localErr := client.NFInstancesStoreApi.SearchNFInstances(ctx, request)
	if res != nil && res.Nrf_NFDisc_SearchResult != nil {
		result = *res.Nrf_NFDisc_SearchResult
	}
	return result, localErr
}

func (s *nnrfService) NFDiscoveryPCF(ctx context.Context) (
	result models.Nrf_NFDisc_SearchResult, localErr error,
) {
	targetNfType := models.Nrf_NFMgmt_NFType_PCF
	requesterNfType := models.Nrf_NFMgmt_NFType_SMF
	request := &NFDisc.SearchNFInstancesRequest{
		TargetNfType:    &targetNfType,
		RequesterNfType: &requesterNfType,
	}

	smfContext := s.consumer.Context()

	client := s.getNFDiscoveryClient(smfContext.NrfUri)
	// Check data
	res, localErr := client.NFInstancesStoreApi.SearchNFInstances(ctx, request)
	if res != nil && res.Nrf_NFDisc_SearchResult != nil {
		result = *res.Nrf_NFDisc_SearchResult
	}
	return result, localErr
}

func (s *nnrfService) NFDiscoveryAMF(smContext *smf_context.SMContext, ctx context.Context) (
	result models.Nrf_NFDisc_SearchResult, localErr error,
) {
	targetNfType := models.Nrf_NFMgmt_NFType_AMF
	requesterNfType := models.Nrf_NFMgmt_NFType_SMF
	request := &NFDisc.SearchNFInstancesRequest{
		TargetNfType:       &targetNfType,
		RequesterNfType:    &requesterNfType,
		TargetNfInstanceId: &smContext.ServingNfId,
	}

	smfContext := s.consumer.Context()

	client := s.getNFDiscoveryClient(smfContext.NrfUri)
	// Check data
	res, localErr := client.NFInstancesStoreApi.SearchNFInstances(ctx, request)
	if res != nil && res.Nrf_NFDisc_SearchResult != nil {
		result = *res.Nrf_NFDisc_SearchResult
	}
	return result, localErr
}

func (s *nnrfService) SendNFDiscoveryUDM() (*models.ProblemDetails, error) {
	smfContext := s.consumer.Context()
	ctx, pd, err := smfContext.GetTokenCtxForNRF(models.Nrf_NFMgmt_ServiceName_NNRF_DISC)
	if err != nil {
		return pd, err
	}

	// Check data
	result, localErr := s.NFDiscoveryUDM(ctx)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		switch errModel := err.Model().(type) {
		case NFDisc.SearchNFInstancesError:
			return errModel.ProblemDetails, nil
		case error:
			return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
		default:
			return nil, openapi.ReportError("openapi error")
		}
	case error:
		return openapi.ProblemDetailsSystemFailure(err.Error()), nil
	case nil:
		if len(result.NfInstances) == 0 {
			logger.ConsumerLog.Warnln("UDM discovery returned empty NfInstances")
			return nil, openapi.ReportError("UDM discovery returned empty NfInstances")
		}
		smfContext.UDMProfile = result.NfInstances[0]

		var client *SDM.APIClient
		for _, service := range smfContext.UDMProfile.NfServices {
			if service.ServiceName == models.Nrf_NFMgmt_ServiceName_NUDM_SDM {
				client = s.consumer.nudmService.getSubscribeDataManagementClient(service.ApiPrefix)
			}
		}
		if client == nil {
			logger.ConsumerLog.Traceln("Get Subscribe Data Management Client Failed")
			return nil, fmt.Errorf("get Subscribe Data Management Client Failed")
		}
	default:
		return nil, openapi.ReportError("server no response")
	}

	return nil, nil
}

func (s *nnrfService) SendNFDiscoveryPCF() (*models.ProblemDetails, error) {
	ctx, pd, err := s.consumer.Context().GetTokenCtxForNRF(models.Nrf_NFMgmt_ServiceName_NNRF_DISC)
	if err != nil {
		return pd, err
	}

	// Check data
	result, localErr := s.NFDiscoveryPCF(ctx)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		switch errModel := err.Model().(type) {
		case NFDisc.SearchNFInstancesError:
			return errModel.ProblemDetails, nil
		case error:
			return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
		default:
			return nil, openapi.ReportError("openapi error")
		}
	case error:
		return openapi.ProblemDetailsSystemFailure(err.Error()), nil
	case nil:
		logger.ConsumerLog.Traceln(result.NfInstances)
	default:
		return nil, openapi.ReportError("server no response")
	}

	return nil, nil
}

func (s *nnrfService) SendNFDiscoveryServingAMF(smContext *smf_context.SMContext) (*models.ProblemDetails, error) {
	ctx, pd, err := s.consumer.Context().GetTokenCtxForNRF(models.Nrf_NFMgmt_ServiceName_NNRF_DISC)
	if err != nil {
		return pd, err
	}

	// Check data
	result, localErr := s.NFDiscoveryAMF(smContext, ctx)

	switch err := localErr.(type) {
	case openapi.GenericOpenAPIError:
		switch errModel := err.Model().(type) {
		case NFDisc.SearchNFInstancesError:
			return errModel.ProblemDetails, nil
		case error:
			return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
		default:
			return nil, openapi.ReportError("openapi error")
		}
	case error:
		return openapi.ProblemDetailsSystemFailure(err.Error()), nil
	case nil:
		if result.NfInstances == nil {
			logger.ConsumerLog.Warnln("NfInstances is nil")
			return nil, openapi.ReportError("NfInstances is nil")
		}
		if len(result.NfInstances) == 0 {
			logger.ConsumerLog.Warnln("AMF NfInstances is empty")
			return nil, openapi.ReportError("AMF NfInstances is empty")
		}
		logger.ConsumerLog.Info("SendNFDiscoveryServingAMF ok")
		smContext.AMFProfile = result.NfInstances[0]
	default:
		return nil, openapi.ReportError("server no response")
	}

	return nil, nil
}

// CHFSelection will select CHF for this SM Context
func (s *nnrfService) CHFSelection(smContext *smf_context.SMContext) error {
	// Send NFDiscovery for find CHF
	targetNfType := models.Nrf_NFMgmt_NFType_CHF
	requesterNfType := models.Nrf_NFMgmt_NFType_SMF
	request := &NFDisc.SearchNFInstancesRequest{
		TargetNfType:    &targetNfType,
		RequesterNfType: &requesterNfType,
		// Supi:            &smContext.Supi,
	}

	ctx, _, err := s.consumer.Context().GetTokenCtxForNRF(models.Nrf_NFMgmt_ServiceName_NNRF_DISC)
	if err != nil {
		return err
	}

	client := s.getNFDiscoveryClient(s.consumer.Context().NrfUri)
	// Check data
	res, err := client.NFInstancesStoreApi.SearchNFInstances(ctx, request)
	if err != nil {
		logger.ConsumerLog.Errorf("SearchNFInstances failed: %+v", err)
		return err
	}

	// Select CHF from available CHF
	if res != nil && len(res.Nrf_NFDisc_SearchResult.NfInstances) > 0 {
		smContext.SelectedCHFProfile = res.Nrf_NFDisc_SearchResult.NfInstances[0]
		return nil
	}
	return fmt.Errorf("no CHF found in CHFSelection")
}

// PCFSelection will select PCF for this SM Context
func (s *nnrfService) PCFSelection(smContext *smf_context.SMContext) error {
	ctx, _, errToken := s.consumer.Context().GetTokenCtxForNRF(models.Nrf_NFMgmt_ServiceName_NNRF_DISC)
	if errToken != nil {
		return errToken
	}
	// Send NFDiscovery for find PCF
	targetNfType := models.Nrf_NFMgmt_NFType_PCF
	requesterNfType := models.Nrf_NFMgmt_NFType_SMF
	request := &NFDisc.SearchNFInstancesRequest{
		TargetNfType:    &targetNfType,
		RequesterNfType: &requesterNfType,
	}

	if s.consumer.Context().Locality != "" {
		request.PreferredLocality = &s.consumer.Context().Locality
	}

	client := s.getNFDiscoveryClient(s.consumer.Context().NrfUri)
	// Check data
	res, err := client.NFInstancesStoreApi.SearchNFInstances(ctx, request)
	if err != nil {
		return err
	}

	// Select PCF from available PCF
	if res == nil || len(res.Nrf_NFDisc_SearchResult.NfInstances) == 0 {
		return fmt.Errorf("no PCF found in PCFSelection")
	}
	smContext.SelectedPCFProfile = res.Nrf_NFDisc_SearchResult.NfInstances[0]

	return nil
}

func (s *nnrfService) SearchNFInstances(
	ctx context.Context,
	targetNfType, requesterNfType models.Nrf_NFMgmt_NFType,
	localVarOptionals *NFDisc.SearchNFInstancesRequest,
) (*models.Nrf_NFDisc_SearchResult, error) {
	client := s.getNFDiscoveryClient(s.consumer.Context().NrfUri)

	request := &NFDisc.SearchNFInstancesRequest{
		TargetNfType:    &targetNfType,
		RequesterNfType: &requesterNfType,
	}

	res, err := client.NFInstancesStoreApi.SearchNFInstances(ctx, request)
	if err != nil {
		return nil, err
	}

	return res.Nrf_NFDisc_SearchResult, err
}
