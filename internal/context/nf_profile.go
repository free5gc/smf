package context

import (
	"time"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

type NFProfile struct {
	NFServices       *[]models.Nrf_NFMgmt_NFService
	NFServiceVersion *[]models.Nrf_NFMgmt_NFServiceVersion
	SMFInfo          *models.Nrf_NFMgmt_SmfInfo
	PLMNList         *[]models.PlmnId
}

func (c *SMFContext) SetupNFProfile(nfProfileconfig *factory.Config) {
	// Set time
	nfSetupTime := time.Now()

	// set NfServiceVersion
	c.NfProfile.NFServiceVersion = &[]models.Nrf_NFMgmt_NFServiceVersion{
		{
			ApiVersionInUri: "v1",
			ApiFullVersion:  nfProfileconfig.GetVersion(),
			Expiry:          &nfSetupTime,
		},
	}

	// set NFServices
	c.NfProfile.NFServices = new([]models.Nrf_NFMgmt_NFService)
	for _, serviceName := range nfProfileconfig.Configuration.ServiceNameList {
		*c.NfProfile.NFServices = append(*c.NfProfile.NFServices, models.Nrf_NFMgmt_NFService{
			ServiceInstanceId: GetSelf().NfInstanceID + serviceName,
			ServiceName:       models.Nrf_NFMgmt_ServiceName(serviceName),
			Versions:          *c.NfProfile.NFServiceVersion,
			Scheme:            GetSelf().URIScheme,
			NfServiceStatus:   models.Nrf_NFMgmt_NFServiceStatus_REGISTERED,
			IpEndPoints: []models.Nrf_NFMgmt_IpEndPoint{
				{
					Ipv4Address: GetSelf().RegisterIPv4,
					Port:        int32(GetSelf().SBIPort),
				},
			},
		})
	}

	// set smfInfo
	c.NfProfile.SMFInfo = &models.Nrf_NFMgmt_SmfInfo{
		SNssaiSmfInfoList: SNssaiSmfInfo(),
	}

	// set PlmnList if exists
	if plmnList := nfProfileconfig.Configuration.PLMNList; plmnList != nil {
		c.NfProfile.PLMNList = new([]models.PlmnId)
		for _, plmn := range plmnList {
			*c.NfProfile.PLMNList = append(*c.NfProfile.PLMNList, models.PlmnId{
				Mcc: plmn.Mcc,
				Mnc: plmn.Mnc,
			})
		}
	}
}

func SNssaiSmfInfo() []models.Nrf_NFMgmt_SnssaiSmfInfoItem {
	snssaiInfo := make([]models.Nrf_NFMgmt_SnssaiSmfInfoItem, 0)
	for _, snssai := range smfContext.SnssaiInfos {
		var snssaiInfoModel models.Nrf_NFMgmt_SnssaiSmfInfoItem
		snssaiInfoModel.SNssai = &models.ExtSnssai{
			Sst: snssai.Snssai.Sst,
			Sd:  snssai.Snssai.Sd,
		}
		dnnModelList := make([]models.Nrf_NFMgmt_DnnSmfInfoItem, 0)

		for dnn := range snssai.DnnInfos {
			dnnModelList = append(dnnModelList, models.Nrf_NFMgmt_DnnSmfInfoItem{
				Dnn: dnn,
			})
		}

		snssaiInfoModel.DnnSmfInfoList = dnnModelList

		snssaiInfo = append(snssaiInfo, snssaiInfoModel)
	}
	return snssaiInfo
}
