package util

import (
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/util/serviceuri"
	"log"
)

// Preserve the existing NF-facing API while centralizing URI construction.
type ServiceURIOptions = serviceuri.Options

// Preserve errors.Is behavior in the existing NFService selector.
var errNFServiceAddressUnavailable = serviceuri.ErrAddressUnavailable

func toSharedIPPoints(points []models.Nrf_NFMgmt_IpEndPoint) []serviceuri.Endpoint {
	out := make([]serviceuri.Endpoint, 0, len(points))
	for _, p := range points {
		out = append(out, serviceuri.Endpoint{IPv4Address: p.Ipv4Address, IPv6Address: p.Ipv6Address, Port: p.Port})
	}
	return out
}

func buildNFServiceURI(profile models.Nrf_NFDisc_NFProfile,
	service models.Nrf_NFDisc_NFService, opts ServiceURIOptions,
) (string, error) {
	result, err := serviceuri.Build(
		serviceuri.Profile{
			FQDN:          profile.Fqdn,
			IPv4Addresses: profile.Ipv4Addresses,
			IPv6Addresses: profile.Ipv6Addresses,
		},
		serviceuri.Service{
			Scheme:    string(service.Scheme),
			FQDN:      service.Fqdn,
			APIPrefix: service.ApiPrefix,
			Endpoints: toSharedIPPoints(service.IpEndPoints),
		}, opts,
	)
	if err == nil && result.UsedLegacyAbsoluteAPIPrefix {
		log.Printf("Deprecated absolute NFService.apiPrefix accepted for compatibility: %q", service.ApiPrefix)
	}
	return result.APIRoot, err
}

// The helpers remain available for existing NF-local tests, while the logic
// is implemented only in github.com/free5gc/util/serviceuri.
func validateIPPoint(p models.Nrf_NFMgmt_IpEndPoint) error {
	return serviceuri.ValidateIPPoint(serviceuri.Endpoint{
		IPv4Address: p.Ipv4Address, IPv6Address: p.Ipv6Address, Port: p.Port,
	})
}

func portForFQDN(points []models.Nrf_NFMgmt_IpEndPoint) (int32, error) {
	return serviceuri.PortForFQDN(toSharedIPPoints(points))
}

func validateAPIPrefix(prefix string) error {
	return serviceuri.ValidateAPIPrefix(prefix)
}

func buildAPIRoot(scheme, host string, port int32, prefix string) (string, error) {
	return serviceuri.BuildAPIRoot(scheme, host, port, prefix)
}
