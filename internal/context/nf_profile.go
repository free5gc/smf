package context

import (
	"fmt"
	"time"
	"net/netip"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

type NFProfile struct {
	NFServices       *[]models.NrfNfManagementNfService
	NFServiceVersion *[]models.NfServiceVersion
	SMFInfo          *models.SmfInfo
	PLMNList         *[]models.PlmnId
}

func (c *SMFContext) SetupNFProfile(nfProfileconfig *factory.Config) {
	IPUri := netip.AddrPortFrom(c.RegisterIP, uint16(c.SBIPort)).String()

	// Set time
	nfSetupTime := time.Now()

	// set NfServiceVersion
	c.NfProfile.NFServiceVersion = &[]models.NfServiceVersion{
		{
			ApiVersionInUri: "v1",
			ApiFullVersion: fmt.
				Sprintf("https://%s"+factory.SmfPdusessionResUriPrefix, IPUri),
			Expiry: &nfSetupTime,
		},
	}

	// set NFServices
	c.NfProfile.NFServices = new([]models.NrfNfManagementNfService)
	for _, serviceName := range nfProfileconfig.Configuration.ServiceNameList {
		*c.NfProfile.NFServices = append(*c.NfProfile.NFServices, models.NrfNfManagementNfService{
			ServiceInstanceId: c.NfInstanceID + serviceName,
			ServiceName:       models.ServiceName(serviceName),
			Versions:          *c.NfProfile.NFServiceVersion,
			Scheme:            models.UriScheme_HTTPS,
			NfServiceStatus:   models.NfServiceStatus_REGISTERED,
			ApiPrefix:         fmt.Sprintf(c.GetIPUri()),
			IpEndPoints:       c.GetIpEndPoint(),
		})
	}

	// set smfInfo
	c.NfProfile.SMFInfo = &models.SmfInfo{
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

func (context *SMFContext) GetIPUri() string {
	addr := context.RegisterIP
	port := uint16(context.SBIPort)
	scheme := string(context.URIScheme)

	bind := netip.AddrPortFrom(addr, port).String()

	return scheme + "://" + bind
}

func (context *SMFContext) GetIpEndPoint() []models.IpEndPoint {
	if context.RegisterIP.Is6() {
		return []models.IpEndPoint{
			{
				Ipv6Address: context.RegisterIP.String(),
				Transport:   models.NrfNfManagementTransportProtocol_TCP,
				Port:        int32(context.SBIPort),
			},
		}
	} else if context.RegisterIP.Is4() {
		return []models.IpEndPoint{
			{
				Ipv4Address: context.RegisterIP.String(),
				Transport:   models.NrfNfManagementTransportProtocol_TCP,
				Port:        int32(context.SBIPort),
			},
		}
	}
	return nil
}

func SNssaiSmfInfo() []models.SnssaiSmfInfoItem {
	snssaiInfo := make([]models.SnssaiSmfInfoItem, 0)
	for _, snssai := range smfContext.SnssaiInfos {
		var snssaiInfoModel models.SnssaiSmfInfoItem
		snssaiInfoModel.SNssai = &models.ExtSnssai{
			Sst: snssai.Snssai.Sst,
			Sd:  snssai.Snssai.Sd,
		}
		dnnModelList := make([]models.DnnSmfInfoItem, 0)

		for dnn := range snssai.DnnInfos {
			dnnModelList = append(dnnModelList, models.DnnSmfInfoItem{
				Dnn: dnn,
			})
		}

		snssaiInfoModel.DnnSmfInfoList = dnnModelList

		snssaiInfo = append(snssaiInfo, snssaiInfoModel)
	}
	return snssaiInfo
}
