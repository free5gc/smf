package chf_util

import (
	"context"
	"fmt"
	"net/http"

	smf_context "github.com/free5gc/smf/context"
	"github.com/free5gc/smf/logger"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nchf_ConvergedCharging"
	"github.com/free5gc/openapi/Nnrf_NFDiscovery"
	"github.com/free5gc/openapi/models"
)

// SearchNFServiceUri returns NF Uri derived from NfProfile with corresponding service
func searchNFServiceUri(nfProfile models.NfProfile, serviceName models.ServiceName,
	nfServiceStatus models.NfServiceStatus) (nfUri string) {
	if nfProfile.NfServices != nil {
		for _, service := range *nfProfile.NfServices {
			if service.ServiceName == serviceName && service.NfServiceStatus == nfServiceStatus {
				if nfProfile.Fqdn != "" {
					nfUri = nfProfile.Fqdn
				} else if service.Fqdn != "" {
					nfUri = service.Fqdn
				} else if service.ApiPrefix != "" {
					nfUri = service.ApiPrefix
				} else if service.IpEndPoints != nil {
					point := (*service.IpEndPoints)[0]
					if point.Ipv4Address != "" {
						nfUri = getSbiUri(service.Scheme, point.Ipv4Address, point.Port)
					} else if len(nfProfile.Ipv4Addresses) != 0 {
						nfUri = getSbiUri(service.Scheme, nfProfile.Ipv4Addresses[0], point.Port)
					}
				}
			}
			if nfUri != "" {
				break
			}
		}
	}

	return
}

func getSbiUri(scheme models.UriScheme, ipv4Address string, port int32) (uri string) {
	if port != 0 {
		uri = fmt.Sprintf("%s://%s:%d", scheme, ipv4Address, port)
	} else {
		switch scheme {
		case models.UriScheme_HTTP:
			uri = fmt.Sprintf("%s://%s:80", scheme, ipv4Address)
		case models.UriScheme_HTTPS:
			uri = fmt.Sprintf("%s://%s:443", scheme, ipv4Address)
		}
	}
	return
}

// getCHFUri will return the first CHF uri retrived from NRF
func getCHFUri() string {
	logger.ExtensionLog.Infof("Find available CHF Servers\n")
	// Set targetNfType
	targetNfType := models.NfType_CHF
	// Set requestNfType
	requesterNfType := models.NfType_SMF
	localVarOptionals := Nnrf_NFDiscovery.SearchNFInstancesParamOpts{}

	// Check data
	result, _, localErr := smf_context.SMF_Self().
		NFDiscoveryClient.
		NFInstancesStoreApi.
		SearchNFInstances(context.TODO(), targetNfType, requesterNfType, &localVarOptionals)

	if localErr == nil {
		for _, nfProfile := range result.NfInstances {
			uri := searchNFServiceUri(nfProfile, models.ServiceName_NCHF_CONVERGEDCHARGING, models.NfServiceStatus_REGISTERED)
			if uri != "" {
				logger.ExtensionLog.Infof("Found CHF uri %s", uri)
				return uri
			}
		}
	} else {
		logger.ExtensionLog.Error(localErr)
		return ""
	}
	return ""
}

func getNchfClient(uri string) *Nchf_ConvergedCharging.APIClient {
	configuration := Nchf_ConvergedCharging.NewConfiguration()
	configuration.SetBasePath(uri)
	client := Nchf_ConvergedCharging.NewAPIClient(configuration)
	return client
}

func CreateInitialChargingData(smPlmnID *models.PlmnId, smContext *smf_context.SMContext) (*models.ChargingDataResponse, error) {
	// Get chf uri
	uri := getCHFUri()

	// Init CHF client
	client := getNchfClient(uri)

	// TODO: Set chargingDataResponse properly. Currently using null data or data that i'm unsure.
	nfConsumerIdentification := models.NFConsumerIdentification{
		NodeFunctionality: models.NodeFunctionality__SMF,
		NfName:            smf_context.SMF_Self().NfInstanceID,
		NfIpv4Address:     smf_context.SMF_Self().RegisterIPv4, //Unsure
		NfPlmnID:          *smPlmnID,
	}

	chargingDataRequest := models.ChargingDataRequest{
		SubscriberIdentifier:     smContext.Supi,
		NfConsumerIdentification: nfConsumerIdentification,
		// InvocationTimeStamp:             time.Now().UTC(),
		InvocationSequenceNumber:        1,                           //Unsure
		OneTimeEvent:                    false,                       //Unsure
		NotifyUri:                       smContext.SmStatusNotifyUri, //Unsure
		ServiceSpecificationInformation: "TS 32.255 release 15",      //Unsure: From TS 132 290: This field identifies the technical specification for the service(e.g. TS 32.255) and release version (e.g. Release 16) that applies to the request. It is for information.
		MultipleUnitUsage:               []models.MultipleUnitUsage{},
		Triggers:                        []models.Trigger{},
	}

	var response *http.Response
	chargingDataResponse, response, err := client.DefaultApi.Create(context.Background(), chargingDataRequest)
	logger.ExtensionLog.Infof("chargingDataResponse %+v", chargingDataResponse)

	if err != nil || response == nil || response.StatusCode != http.StatusCreated {
		logger.ExtensionLog.Warnf("CHF initial charging data request failed")
		return nil, openapi.ReportError("CHF initial charging data request failed[%s]", err.Error())
	}
	defer func() {
		if rspCloseErr := response.Body.Close(); rspCloseErr != nil {
			logger.ExtensionLog.Errorf(
				"ConvergedChargingCreate response body cannot close: %+v", rspCloseErr)
		}
	}()
	return &chargingDataResponse, nil
}
