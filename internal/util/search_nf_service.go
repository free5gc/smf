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

// SelectNFServices selects an NF instance from an NRF discovery result that
// provides every service in names, for procedures that use all of them on the
// same NF instance (e.g. UDM UECM and SDM).
// Each instance is checked on its own; the complete ones are then ordered by
// priority and capacity as in SelectNFService. It returns the instance with the
// API roots in the order of names, or (nil, nil) if no instance provides them all.
func SelectNFServices(
	instances []models.Nrf_NFDisc_NFProfile, names ...models.Nrf_NFMgmt_ServiceName,
) (*models.Nrf_NFDisc_NFProfile, []string) {
	var complete []models.Nrf_NFDisc_NFProfile
next:
	for i := range instances {
		for _, name := range names {
			if SearchNFServiceUri(&instances[i], name) == "" {
				continue next
			}
		}
		complete = append(complete, instances[i])
	}
	if len(complete) == 0 || len(names) == 0 {
		return nil, nil
	}

	profile, _ := SelectNFService(complete, names[0])
	uris := make([]string, len(names))
	for i, name := range names {
		uris[i] = SearchNFServiceUri(profile, name)
	}
	return profile, uris
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
