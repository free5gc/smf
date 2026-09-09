package context

import (
	"slices"

	"github.com/free5gc/openapi/models"
)

const (
	ServiceNameNamfCallback models.Nrf_NFMgmt_ServiceName = "namf-callback"
	ServiceNameNsmfCallback models.Nrf_NFMgmt_ServiceName = "nsmf-callback"
	ServiceNameNnefCallback models.Nrf_NFMgmt_ServiceName = "nnef-callback"
)

var servicePolicies = map[models.Nrf_NFMgmt_ServiceName][]models.Nrf_NFMgmt_NFType{
	models.Nrf_NFMgmt_ServiceName_NSMF_PDUSESSION: {
		models.Nrf_NFMgmt_NFType_AMF,
		models.Nrf_NFMgmt_NFType_SMF,
	},
	models.Nrf_NFMgmt_ServiceName_NSMF_EVENT_EXPOSURE: {
		models.Nrf_NFMgmt_NFType_AMF,
		models.Nrf_NFMgmt_NFType_NEF,
		models.Nrf_NFMgmt_NFType_AF,
		models.Nrf_NFMgmt_NFType_UDM,
		models.Nrf_NFMgmt_NFType_NWDAF,
		models.Nrf_NFMgmt_NFType_DCCF,
	},
	ServiceNameNsmfCallback: {
		models.Nrf_NFMgmt_NFType_PCF,
		models.Nrf_NFMgmt_NFType_CHF,
	},
	// OAM authorization is intentionally unchanged until management-plane
	// authentication is designed separately.
	models.Nrf_NFMgmt_ServiceName_NSMF_OAM: nil,
}

func AllowedNfTypesForService(serviceName models.Nrf_NFMgmt_ServiceName) (
	[]models.Nrf_NFMgmt_NFType, bool,
) {
	allowed, known := servicePolicies[serviceName]
	return slices.Clone(allowed), known
}
