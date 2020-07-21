/*
 * AMF Configuration Factory
 */

package factory

import "free5gc/lib/openapi/models"

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
	Scheme   string `yaml:"scheme"`
	TLS      *TLS   `yaml:"tls"`
	IPv4Addr string `yaml:"ipv4Addr,omitempty"`
	// IPv6Addr string `yaml:"ipv6Addr,omitempty"`
	Port int `yaml:"port,omitempty"`
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

type RoutingConfig struct {
	Info *Info `yaml:"info"`

	UERoutingInfo []*UERoutingInfo `yaml:"ueRoutingInfo"`
}

// UserPlaneInformation describe core network userplane information
type UserPlaneInformation struct {
	UPNodes map[string]UPNode `yaml:"up_nodes"`
	Links   []UPLink          `yaml:"links"`
}

// UPNode represent the user plane node
type UPNode struct {
	Type         string `yaml:"type"`
	NodeID       string `yaml:"node_id"`
	UPResourceIP string `yaml:"node"`
	ANIP         string `yaml:"an_ip"`
	Dnn          string `yaml:"dnn"`
}

type UPLink struct {
	A string `yaml:"A"`
	B string `yaml:"B"`
}
