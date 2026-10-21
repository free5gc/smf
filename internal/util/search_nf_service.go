package util

import (
	"errors"
	"fmt"
	"log"
	"sort"

	"github.com/free5gc/openapi/models"
)

func SearchNFServiceUri(nfProfile *models.Nrf_NFDisc_NFProfile, serviceName models.Nrf_NFMgmt_ServiceName,
	nfServiceStatus models.Nrf_NFMgmt_NFServiceStatus,
) (nfUri string) {
	if nfProfile == nil {
		return ""
	}
	uri, err := ResolveNFServiceURI(*nfProfile, serviceName, nfServiceStatus,
		ServiceURIOptions{AllowLegacyAbsoluteAPIPrefix: true})
	if err != nil {
		log.Printf("[WARN][SMF] NFService URI resolution failed: %v", err)
		return ""
	}
	return uri
}

// ResolveNFServiceURI builds an NF service API root using 3GPP NRF discovery fields.
func ResolveNFServiceURI(profile models.Nrf_NFDisc_NFProfile, name models.Nrf_NFMgmt_ServiceName,
	status models.Nrf_NFMgmt_NFServiceStatus, opts ServiceURIOptions,
) (string, error) {
	var lastAddressError error
	for _, service := range profile.NfServices {
		if service.ServiceName != name || service.NfServiceStatus != status {
			continue
		}
		uri, err := buildNFServiceURI(profile, service, opts)
		if errors.Is(err, errNFServiceAddressUnavailable) {
			lastAddressError = err
			continue
		}
		return uri, err
	}
	keys := make([]string, 0, len(profile.NfServiceList))
	for key := range profile.NfServiceList {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		service := profile.NfServiceList[key]
		if service.ServiceName != name || service.NfServiceStatus != status {
			continue
		}
		uri, err := buildNFServiceURI(profile, service, opts)
		if errors.Is(err, errNFServiceAddressUnavailable) {
			lastAddressError = err
			continue
		}
		return uri, err
	}
	if lastAddressError != nil {
		return "", lastAddressError
	}
	return "", fmt.Errorf("NF service %q with status %q not found", name, status)
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
