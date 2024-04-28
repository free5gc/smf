package context

import (
	"fmt"
	"time"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

type NFProfile struct {
	NFServices       *[]models.NfService
	NFServiceVersion *[]models.NfServiceVersion
	SMFInfo          *models.SmfInfo
	PLMNList         *[]models.PlmnId
}

func (c *SMFContext) SetupNFProfile(NFProfileconfig *factory.Config) {
	// Set time
	nfSetupTime := time.Now()

	// set NfServiceVersion
	c.NfProfile.NFServiceVersion = &[]models.NfServiceVersion{
		{
			ApiVersionInUri: "v1",
			ApiFullVersion: fmt.
				Sprintf("https://%s:%d"+factory.SmfPdusessionResUriPrefix, GetSelf().RegisterIPv4, GetSelf().SBIPort),
			Expiry: &nfSetupTime,
		},
	}

	// set NFServices
	c.NfProfile.NFServices = new([]models.NfService)
	for _, serviceName := range NFProfileconfig.Configuration.ServiceNameList {
		*c.NfProfile.NFServices = append(*c.NfProfile.NFServices, models.NfService{
			ServiceInstanceId: GetSelf().NfInstanceID + serviceName,
			ServiceName:       models.ServiceName(serviceName),
			Versions:          c.NfProfile.NFServiceVersion,
			Scheme:            models.UriScheme_HTTPS,
			NfServiceStatus:   models.NfServiceStatus_REGISTERED,
			ApiPrefix:         fmt.Sprintf("%s://%s:%d", GetSelf().URIScheme, GetSelf().RegisterIPv4, GetSelf().SBIPort),
		})
	}

	// set smfInfo
	c.NfProfile.SMFInfo = &models.SmfInfo{
		SNssaiSmfInfoList: SNssaiSmfInfo(),
	}

	// set PlmnList if exists
	if plmnList := NFProfileconfig.Configuration.PLMNList; plmnList != nil {
		c.NfProfile.PLMNList = new([]models.PlmnId)
		for _, plmn := range plmnList {
			*c.NfProfile.PLMNList = append(*c.NfProfile.PLMNList, models.PlmnId{
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
