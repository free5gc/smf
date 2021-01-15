package context

import (
	"fmt"
	"free5gc/lib/openapi/models"
	"free5gc/src/smf/factory"
	"time"
)

var NFServices *[]models.NfService

var NfServiceVersion *[]models.NfServiceVersion

var SmfInfo *models.SmfInfo

func SetupNFProfile(config *factory.Config) {
	//Set time
	nfSetupTime := time.Now()

	//set NfServiceVersion
	NfServiceVersion = &[]models.NfServiceVersion{
		{
			ApiVersionInUri: "v1",
			ApiFullVersion:  fmt.Sprintf("https://%s:%d/nsmf-pdusession/v1", SMF_Self().RegisterIPv4, SMF_Self().SBIPort),
			Expiry:          &nfSetupTime,
		},
	}

	//set NFServices
	NFServices = new([]models.NfService)
	for _, serviceName := range config.Configuration.ServiceNameList {
		*NFServices = append(*NFServices, models.NfService{
			ServiceInstanceId: SMF_Self().NfInstanceID + serviceName,
			ServiceName:       models.ServiceName(serviceName),
			Versions:          NfServiceVersion,
			Scheme:            models.UriScheme_HTTPS,
			NfServiceStatus:   models.NfServiceStatus_REGISTERED,
			ApiPrefix:         fmt.Sprintf("%s://%s:%d", SMF_Self().URIScheme, SMF_Self().RegisterIPv4, SMF_Self().SBIPort),
		})
	}

	//set smfInfo
	SmfInfo = &models.SmfInfo{
		SNssaiSmfInfoList: &smfContext.SnssaiInfos,
	}
}
