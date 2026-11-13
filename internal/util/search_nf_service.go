package util

import (
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/util/serviceuri"
)

// NFs that still register a full URL as apiPrefix remain reachable.
var serviceURIOptions = serviceuri.Options{AllowLegacyAbsoluteAPIPrefix: true}

// SelectNFService selects an NF instance from an NRF discovery result and
// returns it with the API root of service name, or (nil, "") if none is usable.
func SelectNFService(
	instances []models.Nrf_NFDisc_NFProfile, name models.Nrf_NFMgmt_ServiceName,
) (*models.Nrf_NFDisc_NFProfile, string) {
	profile, uri, err := serviceuri.ResolveFromSearchResult(instances, name, serviceURIOptions)
	if err != nil {
		logger.UtilLog.Warnf("NF service %s API root resolution failed: %v", name, err)
		return nil, ""
	}
	return profile, uri
}

// SearchNFServiceUri returns the API root of service name in nfProfile, or "".
func SearchNFServiceUri(nfProfile *models.Nrf_NFDisc_NFProfile, name models.Nrf_NFMgmt_ServiceName) string {
	uri, err := serviceuri.ResolveNFServiceURI(nfProfile, name, serviceURIOptions)
	if err != nil {
		logger.UtilLog.Warnf("NF service %s API root resolution failed: %v", name, err)
		return ""
	}
	return uri
}
