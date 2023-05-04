package consumer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/antihax/optional"
	"github.com/mohae/deepcopy"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nnrf_NFDiscovery"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

func SendNFRegistration() error {
	smfProfile := smf_context.NFProfile

	sNssais := []models.Snssai{}
	for _, snssaiSmfInfo := range *smfProfile.SMFInfo.SNssaiSmfInfoList {
		sNssais = append(sNssais, *snssaiSmfInfo.SNssai)
	}

	// set nfProfile
	profile := models.NfProfile{
		NfInstanceId:  smf_context.GetSelf().NfInstanceID,
		NfType:        models.NfType_SMF,
		NfStatus:      models.NfStatus_REGISTERED,
		Ipv4Addresses: []string{smf_context.GetSelf().RegisterIPv4},
		NfServices:    smfProfile.NFServices,
		SmfInfo:       smfProfile.SMFInfo,
		SNssais:       &sNssais,
		PlmnList:      smfProfile.PLMNList,
	}
	if smf_context.GetSelf().Locality != "" {
		profile.Locality = smf_context.GetSelf().Locality
	}
	var rep models.NfProfile
	var res *http.Response
	var err error

	// Check data (Use RESTful PUT)
	for {
		rep, res, err = smf_context.GetSelf().
			NFManagementClient.
			NFInstanceIDDocumentApi.
			RegisterNFInstance(context.TODO(), smf_context.GetSelf().NfInstanceID, profile)
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
			smf_context.GetSelf().NfInstanceID = resourceUri[strings.LastIndex(resourceUri, "/")+1:]
			break
		} else {
			logger.ConsumerLog.Infof("handler returned wrong status code %d", status)
			// fmt.Errorf("NRF return wrong status code %d", status)
		}
	}

	logger.InitLog.Infof("SMF Registration to NRF %v", rep)
	return nil
}

func RetrySendNFRegistration(MaxRetry int) error {
	retryCount := 0
	for retryCount < MaxRetry {
		err := SendNFRegistration()
		if err == nil {
			return nil
		}
		logger.ConsumerLog.Warnf("Send NFRegistration Failed by %v", err)
		retryCount++
	}

	return fmt.Errorf("[SMF] Retry NF Registration has meet maximum")
}

func SendNFDeregistration() error {
	// Check data (Use RESTful DELETE)
	res, localErr := smf_context.GetSelf().
		NFManagementClient.
		NFInstanceIDDocumentApi.
		DeregisterNFInstance(context.TODO(), smf_context.GetSelf().NfInstanceID)
	if localErr != nil {
		logger.ConsumerLog.Warnln(localErr)
		return localErr
	}
	defer func() {
		if resCloseErr := res.Body.Close(); resCloseErr != nil {
			logger.ConsumerLog.Errorf("DeregisterNFInstance response body cannot close: %+v", resCloseErr)
		}
	}()
	if res != nil {
		if status := res.StatusCode; status != http.StatusNoContent {
			logger.ConsumerLog.Warnln("handler returned wrong status code ", status)
			return openapi.ReportError("handler returned wrong status code %d", status)
		}
	}
	return nil
}

func SendNFDiscoveryUDM() (*models.ProblemDetails, error) {
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	// Check data
	result, httpResp, localErr := smf_context.GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(context.TODO(), models.NfType_UDM, models.NfType_SMF, &localVarOptionals)

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
	// Set targetNfType
	targetNfType := models.NfType_PCF
	// Set requestNfType
	requesterNfType := models.NfType_SMF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	// Check data
	result, httpResp, localErr := smf_context.GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(context.TODO(), targetNfType, requesterNfType, &localVarOptionals)

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
			return
		}
		problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		problemDetails = &problem
	} else {
		err = openapi.ReportError("server no response")
	}

	return problemDetails, err
}

func SendNFDiscoveryServingAMF(smContext *smf_context.SMContext) (*models.ProblemDetails, error) {
	targetNfType := models.NfType_AMF
	requesterNfType := models.NfType_SMF

	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	localVarOptionals.TargetNfInstanceId = optional.NewInterface(smContext.ServingNfId)

	// Check data
	result, httpResp, localErr := smf_context.GetSelf().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(context.TODO(), targetNfType, requesterNfType, &localVarOptionals)

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

func SendDeregisterNFInstance() (*models.ProblemDetails, error) {
	logger.ConsumerLog.Infof("Send Deregister NFInstance")

	smfSelf := smf_context.GetSelf()
	// Set client and set url

	res, err := smfSelf.
		NFManagementClient.
		NFInstanceIDDocumentApi.
		DeregisterNFInstance(context.Background(), smfSelf.NfInstanceID)
	if err == nil {
		return nil, err
	} else if res != nil {
		defer func() {
			if resCloseErr := res.Body.Close(); resCloseErr != nil {
				logger.ConsumerLog.Errorf("DeregisterNFInstance response body cannot close: %+v", resCloseErr)
			}
		}()
		if res.Status != err.Error() {
			return nil, err
		}
		problem := err.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		return &problem, err
	} else {
		return nil, openapi.ReportError("server no response")
	}
}
