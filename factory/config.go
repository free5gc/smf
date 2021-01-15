/*
 * AMF Configuration Factory
 */

package factory

import (
	"free5gc/lib/openapi/models"
	"time"
)

type Config struct {
	Info Info `yaml:"info"`

	Configuration Configuration `yaml:"configuration"`
}

type Info struct {
	Version string `yaml:"version,omitempty"`

	Description string `yaml:"description,omitempty"`
}

type Configuration struct {
	SmfName string `yaml:"smfName,omitempty"`

	Sbi *Sbi `yaml:"sbi,omitempty"`

	PFCP *PFCP `yaml:"pfcp,omitempty"`

	DNN map[string]DNNInfo `yaml:"dnn,omitempty"`

	NrfUri string `yaml:"nrfUri,omitempty"`

	UserPlaneInformation UserPlaneInformation `yaml:"userplane_information"`

	UESubnet string `yaml:"ue_subnet"`

	ServiceNameList []string `yaml:"serviceNameList,omitempty"`

	SNssaiInfo []models.SnssaiSmfInfoItem `yaml:"snssai_info,omitempty"`

	ULCL bool `yaml:"ulcl,omitempty"`
}

type Sbi struct {
	Scheme       string `yaml:"scheme"`
	TLS          *TLS   `yaml:"tls"`
	RegisterIPv4 string `yaml:"registerIPv4,omitempty"` // IP that is registered at NRF.
	// IPv6Addr string `yaml:"ipv6Addr,omitempty"`
	BindingIPv4 string `yaml:"bindingIPv4,omitempty"` // IP used to run the server in the node.
	Port        int    `yaml:"port,omitempty"`
}

type TLS struct {
	PEM string `yaml:"pem,omitempty"`
	Key string `yaml:"key,omitempty"`
}

type PFCP struct {
	Addr string `yaml:"addr,omitempty"`
	Port uint16 `yaml:"port,omitempty"`
}

type DNNInfo struct {
	DNS DNS `yaml:"dns,omitempty"`
}

type DNS struct {
	IPv4Addr string `yaml:"ipv4,omitempty"`
	IPv6Addr string `yaml:"ipv6,omitempty"`
}

type Path struct {
	DestinationIP string `yaml:"DestinationIP,omitempty"`

	DestinationPort string `yaml:"DestinationPort,omitempty"`

	UPF []string `yaml:"UPF,omitempty"`
}

type UERoutingInfo struct {
	SUPI string `yaml:"SUPI,omitempty"`

	AN string `yaml:"AN,omitempty"`

	PathList []Path `yaml:"PathList,omitempty"`
}

// RouteProfID is string providing a Route Profile identifier.
type RouteProfID string

// RouteProfile maintains the mapping between RouteProfileID and ForwardingPolicyID of UPF
type RouteProfile struct {
	// Forwarding Policy ID of the route profile
	ForwardingPolicyID string `yaml:"forwardingPolicyID,omitempty"`
}

// PfdContent represents the flow of the application
type PfdContent struct {
	// Identifies a PFD of an application identifier.
	PfdID string `yaml:"pfdID,omitempty"`
	// Represents a 3-tuple with protocol, server ip and server port for
	// UL/DL application traffic.
	FlowDescriptions []string `yaml:"flowDescriptions,omitempty"`
	// Indicates a URL or a regular expression which is used to match the
	// significant parts of the URL.
	Urls []string `yaml:"urls,omitempty"`
	// Indicates an FQDN or a regular expression as a domain name matching
	// criteria.
	DomainNames []string `yaml:"domainNames,omitempty"`
}

// PfdDataForApp represents the PFDs for an application identifier
type PfdDataForApp struct {
	// Identifier of an application.
	AppID string `yaml:"applicationId"`
	// PFDs for the application identifier.
	Pfds []PfdContent `yaml:"pfds"`
	// Caching time for an application identifier.
	CachingTime *time.Time `yaml:"cachingTime,omitempty"`
}

type RoutingConfig struct {
	Info *Info `yaml:"info"`

	UERoutingInfo []*UERoutingInfo `yaml:"ueRoutingInfo"`

	RouteProf map[RouteProfID]RouteProfile `yaml:"routeProfile,omitempty"`

	PfdDatas []*PfdDataForApp `yaml:"pfdDataForApp,omitempty"`
}

// UserPlaneInformation describe core network userplane information
type UserPlaneInformation struct {
	UPNodes map[string]UPNode `yaml:"up_nodes"`
	Links   []UPLink          `yaml:"links"`
}

// UPNode represent the user plane node
type UPNode struct {
	Type   string `yaml:"type"`
	NodeID string `yaml:"node_id"`
	ANIP   string `yaml:"an_ip"`
	Dnn    string `yaml:"dnn"`
}

type UPLink struct {
	A string `yaml:"A"`
	B string `yaml:"B"`
}
