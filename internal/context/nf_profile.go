package context

import (
	"fmt"
	"time"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

var NFProfile struct {
	NFServices       *[]models.NfService
	NFServiceVersion *[]models.NfServiceVersion
	SMFInfo          *models.SmfInfo
	PLMNList         *[]models.PlmnId
}

func SetupNFProfile(config *factory.Config) {
	// Set time
	nfSetupTime := time.Now()

	// set NfServiceVersion
	NFProfile.NFServiceVersion = &[]models.NfServiceVersion{
		{
			ApiVersionInUri: "v1",
			ApiFullVersion:  fmt.Sprintf("https://%s:%d/nsmf-pdusession/v1", SMF_Self().RegisterIPv4, SMF_Self().SBIPort),
			Expiry:          &nfSetupTime,
		},
	}

	// set NFServices
	NFProfile.NFServices = new([]models.NfService)
	for _, serviceName := range config.Configuration.ServiceNameList {
		*NFProfile.NFServices = append(*NFProfile.NFServices, models.NfService{
			ServiceInstanceId: SMF_Self().NfInstanceID + serviceName,
			ServiceName:       models.ServiceName(serviceName),
			Versions:          NFProfile.NFServiceVersion,
			Scheme:            models.UriScheme_HTTPS,
			NfServiceStatus:   models.NfServiceStatus_REGISTERED,
			ApiPrefix:         fmt.Sprintf("%s://%s:%d", SMF_Self().URIScheme, SMF_Self().RegisterIPv4, SMF_Self().SBIPort),
		})
	}

	// set smfInfo
	NFProfile.SMFInfo = &models.SmfInfo{
		SNssaiSmfInfoList: SNssaiSmfInfo(),
	}

	// set PlmnList if exists
	if plmnList := config.Configuration.PLMNList; plmnList != nil {
		NFProfile.PLMNList = new([]models.PlmnId)
		for _, plmn := range plmnList {
			*NFProfile.PLMNList = append(*NFProfile.PLMNList, models.PlmnId{
				Mcc: plmn.Mcc,
				Mnc: plmn.Mnc,
			})
		}
	}
}

func SNssaiSmfInfo() *[]models.SnssaiSmfInfoItem {
	snssaiInfo := make([]models.SnssaiSmfInfoItem, 0)
	for _, snssai := range smfContext.SnssaiInfos {
		var snssaiInfoModel models.SnssaiSmfInfoItem
		snssaiInfoModel.SNssai = &models.Snssai{
			Sst: snssai.Snssai.Sst,
			Sd:  snssai.Snssai.Sd,
		}
		dnnModelList := make([]models.DnnSmfInfoItem, 0)

		for dnn := range snssai.DnnInfos {
			dnnModelList = append(dnnModelList, models.DnnSmfInfoItem{
				Dnn: dnn,
			})
		}

		snssaiInfoModel.DnnSmfInfoList = &dnnModelList

		snssaiInfo = append(snssaiInfo, snssaiInfoModel)
	}

	return &snssaiInfo
}
