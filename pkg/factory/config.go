/*
 * SMF Configuration Factory
 */

package factory

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/davecgh/go-spew/spew"
	"inet.af/netaddr"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
)

const (
	SmfDefaultTLSKeyLogPath      = "./log/smfsslkey.log"
	SmfDefaultCertPemPath        = "./cert/smf.pem"
	SmfDefaultPrivateKeyPath     = "./cert/smf.key"
	SmfDefaultConfigPath         = "./config/smfcfg.yaml"
	SmfDefaultUERoutingPath      = "./config/uerouting.yaml"
	SmfSbiDefaultIPv4            = "127.0.0.2"
	SmfSbiDefaultPort            = 8000
	SmfSbiDefaultScheme          = "https"
	SmfDefaultNrfUri             = "https://127.0.0.10:8000"
	SmfEventExposureResUriPrefix = "/nsmf_event-exposure/v1"
	SmfPdusessionResUriPrefix    = "/nsmf-pdusession/v1"
	SmfOamUriPrefix              = "/nsmf-oam/v1"
	SmfCallbackUriPrefix         = "/nsmf-callback"
	NrfDiscUriPrefix             = "/nnrf-disc/v1"
	UdmSdmUriPrefix              = "/nudm-sdm/v1"
	PcfSmpolicycontrolUriPrefix  = "/npcf-smpolicycontrol/v1"
	UpiUriPrefix                 = "/upi/v1"
)

type Config struct {
	Info          *Info          `yaml:"info" valid:"required"`
	Configuration *Configuration `yaml:"configuration" valid:"required"`
	Logger        *Logger        `yaml:"logger" valid:"required"`
	sync.RWMutex
}

func (c *Config) Validate() (bool, error) {
	if configuration := c.Configuration; configuration != nil {
		if result, err := configuration.Validate(); err != nil {
			return result, err
		}
	}

	result, err := govalidator.ValidateStruct(c)
	return result, appendInvalid(err)
}

func (c *Config) Print() {
	spew.Config.Indent = "\t"
	str := spew.Sdump(c.Configuration)
	logger.CfgLog.Infof("==================================================")
	logger.CfgLog.Infof("%s", str)
	logger.CfgLog.Infof("==================================================")
}

type Info struct {
	Version     string `yaml:"version,omitempty" valid:"required,in(1.0.7)"`
	Description string `yaml:"description,omitempty" valid:"type(string)"`
}

func (i *Info) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(i)
	return result, appendInvalid(err)
}

type Configuration struct {
	SmfName              string                `yaml:"smfName" valid:"type(string),required"`
	Sbi                  *Sbi                  `yaml:"sbi" valid:"required"`
	PFCP                 *PFCP                 `yaml:"pfcp" valid:"required"`
	NrfUri               string                `yaml:"nrfUri" valid:"url,required"`
	NrfCertPem           string                `yaml:"nrfCertPem,omitempty" valid:"optional"`
	UserPlaneInformation *UserPlaneInformation `yaml:"userplaneInformation" valid:"required"`
	ServiceNameList      []string              `yaml:"serviceNameList" valid:"required"`
	SNssaiInfo           []*SnssaiInfoItem     `yaml:"snssaiInfos" valid:"required"`
	ULCL                 bool                  `yaml:"ulcl" valid:"type(bool),optional"`
	PLMNList             []PlmnID              `yaml:"plmnList"  valid:"optional"`
	Locality             string                `yaml:"locality" valid:"type(string),optional"`
	UrrPeriod            uint16                `yaml:"urrPeriod,omitempty" valid:"optional"`
	UrrThreshold         uint64                `yaml:"urrThreshold,omitempty" valid:"optional"`
	T3591                *TimerValue           `yaml:"t3591" valid:"required"`
	T3592                *TimerValue           `yaml:"t3592" valid:"required"`
	NwInstFqdnEncoding   bool                  `yaml:"nwInstFqdnEncoding" valid:"type(bool),optional"`
	RequestedUnit        int32                 `yaml:"requestedUnit,omitempty" valid:"optional"`
}

type Logger struct {
	Enable       bool   `yaml:"enable" valid:"type(bool)"`
	Level        string `yaml:"level" valid:"required,in(trace|debug|info|warn|error|fatal|panic)"`
	ReportCaller bool   `yaml:"reportCaller" valid:"type(bool)"`
}

func (c *Configuration) Validate() (bool, error) {
	if sbi := c.Sbi; sbi != nil {
		if result, err := sbi.Validate(); err != nil {
			return result, err
		}
	}

	if pfcp := c.PFCP; pfcp != nil {
		if result, err := pfcp.Validate(); err != nil {
			return result, err
		}
	}

	if userPlaneInformation := c.UserPlaneInformation; userPlaneInformation != nil {
		if result, err := userPlaneInformation.Validate(); err != nil {
			return result, err
		}
	}

	for index, serviceName := range c.ServiceNameList {
		switch {
		case serviceName == "nsmf-pdusession":
		case serviceName == "nsmf-event-exposure":
		case serviceName == "nsmf-oam":
		default:
			err := errors.New("Invalid serviceNameList[" + strconv.Itoa(index) + "]: " +
				serviceName + ", should be nsmf-pdusession, nsmf-event-exposure or nsmf-oam.")
			return false, err
		}
	}

	for _, snssaiInfo := range c.SNssaiInfo {
		if result, err := snssaiInfo.Validate(); err != nil {
			return result, err
		}
	}

	if c.PLMNList != nil {
		for _, plmnId := range c.PLMNList {
			if result, err := plmnId.Validate(); err != nil {
				return result, err
			}
		}
	}

	if t3591 := c.T3591; t3591 != nil {
		if result, err := t3591.Validate(); err != nil {
			return result, err
		}
	}

	if t3592 := c.T3592; t3592 != nil {
		if result, err := t3592.Validate(); err != nil {
			return result, err
		}
	}

	result, err := govalidator.ValidateStruct(c)
	return result, appendInvalid(err)
}

type SnssaiInfoItem struct {
	SNssai   *models.Snssai       `yaml:"sNssai" valid:"required"`
	DnnInfos []*SnssaiDnnInfoItem `yaml:"dnnInfos" valid:"required"`
}

func (s *SnssaiInfoItem) Validate() (bool, error) {
	if snssai := s.SNssai; snssai != nil {
		if result := (snssai.Sst >= 0 && snssai.Sst <= 255); !result {
			err := errors.New("Invalid sNssai.Sst: " + strconv.Itoa(int(snssai.Sst)) + ", should be in range 0~255.")
			return false, err
		}

		if snssai.Sd != "" {
			if result := govalidator.StringMatches(snssai.Sd, "^[0-9A-Fa-f]{6}$"); !result {
				err := errors.New("Invalid sNssai.Sd: " + snssai.Sd +
					", should be 3 bytes hex string and in range 000000~FFFFFF.")
				return false, err
			}
		}
	}

	for _, dnnInfo := range s.DnnInfos {
		if result, err := dnnInfo.Validate(); err != nil {
			return result, err
		}
	}
	result, err := govalidator.ValidateStruct(s)
	return result, appendInvalid(err)
}

type SnssaiDnnInfoItem struct {
	Dnn   string `yaml:"dnn" valid:"type(string),minstringlength(1),required"`
	DNS   *DNS   `yaml:"dns" valid:"required"`
	PCSCF *PCSCF `yaml:"pcscf,omitempty" valid:"optional"`
}

func (s *SnssaiDnnInfoItem) Validate() (bool, error) {
	if dns := s.DNS; dns != nil {
		if result, err := dns.Validate(); err != nil {
			return result, err
		}
	}

	if pcscf := s.PCSCF; pcscf != nil {
		if result, err := pcscf.Validate(); err != nil {
			return result, err
		}
	}

	result, err := govalidator.ValidateStruct(s)
	return result, appendInvalid(err)
}

type Sbi struct {
	Scheme       string `yaml:"scheme" valid:"scheme,required"`
	Tls          *Tls   `yaml:"tls" valid:"optional"`
	RegisterIPv4 string `yaml:"registerIPv4,omitempty" valid:"host,optional"` // IP that is registered at NRF.
	// IPv6Addr string `yaml:"ipv6Addr,omitempty"`
	BindingIPv4 string `yaml:"bindingIPv4,omitempty" valid:"host,required"` // IP used to run the server in the node.
	Port        int    `yaml:"port,omitempty" valid:"port,optional"`
}

func (s *Sbi) Validate() (bool, error) {
	govalidator.TagMap["scheme"] = govalidator.Validator(func(str string) bool {
		return str == "https" || str == "http"
	})

	if tls := s.Tls; tls != nil {
		if result, err := tls.Validate(); err != nil {
			return result, err
		}
	}

	result, err := govalidator.ValidateStruct(s)
	return result, appendInvalid(err)
}

type Tls struct {
	Pem string `yaml:"pem,omitempty" valid:"type(string),minstringlength(1),required"`
	Key string `yaml:"key,omitempty" valid:"type(string),minstringlength(1),required"`
}

func (t *Tls) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(t)
	return result, appendInvalid(err)
}

type PFCP struct {
	ListenAddr   string `yaml:"listenAddr,omitempty" valid:"host,required"`
	ExternalAddr string `yaml:"externalAddr,omitempty" valid:"host,required"`
	NodeID       string `yaml:"nodeID,omitempty" valid:"host,required"`
	// interval at which PFCP Association Setup error messages are output.
	AssocFailAlertInterval time.Duration `yaml:"assocFailAlertInterval,omitempty" valid:"type(time.Duration),optional"`
	AssocFailRetryInterval time.Duration `yaml:"assocFailRetryInterval,omitempty" valid:"type(time.Duration),optional"`
	HeartbeatInterval      time.Duration `yaml:"heartbeatInterval,omitempty" valid:"type(time.Duration),optional"`
}

func (p *PFCP) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(p)
	return result, appendInvalid(err)
}

type DNS struct {
	IPv4Addr string `yaml:"ipv4,omitempty" valid:"ipv4,required"`
	IPv6Addr string `yaml:"ipv6,omitempty" valid:"ipv6,optional"`
}

func (d *DNS) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(d)
	return result, appendInvalid(err)
}

type PCSCF struct {
	IPv4Addr string `yaml:"ipv4,omitempty" valid:"ipv4,required"`
}

func (p *PCSCF) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(p)
	return result, appendInvalid(err)
}

type Path struct {
	DestinationIP   string   `yaml:"DestinationIP,omitempty" valid:"ipv4,required"`
	DestinationPort string   `yaml:"DestinationPort,omitempty" valid:"port,optional"`
	UPF             []string `yaml:"UPF,omitempty" valid:"required"`
}

func (p *Path) Validate() (bool, error) {
	for _, upf := range p.UPF {
		if result := len(upf); result == 0 {
			err := errors.New("Invalid UPF: " + upf + ", should not be empty")
			return false, err
		}
	}

	result, err := govalidator.ValidateStruct(p)
	return result, appendInvalid(err)
}

type UERoutingInfo struct {
	Members       []string       `yaml:"members" valid:"required"`
	AN            string         `yaml:"AN,omitempty" valid:"ipv4,optional"`
	PathList      []Path         `yaml:"PathList,omitempty" valid:"optional"`
	Topology      []UPLink       `yaml:"topology" valid:"required"`
	SpecificPaths []SpecificPath `yaml:"specificPath,omitempty" valid:"optional"`
}

func (u *UERoutingInfo) Validate() (bool, error) {
	for _, member := range u.Members {
		if result := govalidator.StringMatches(member, "imsi-[0-9]{5,15}$"); !result {
			err := errors.New("Invalid member (SUPI): " + member)
			return false, err
		}
	}

	for _, path := range u.PathList {
		if result, err := path.Validate(); err != nil {
			return result, err
		}
	}

	for _, path := range u.SpecificPaths {
		if result, err := path.Validate(); err != nil {
			return result, err
		}
	}

	result, err := govalidator.ValidateStruct(u)
	return result, appendInvalid(err)
}

// RouteProfID is string providing a Route Profile identifier.
type RouteProfID string

// RouteProfile maintains the mapping between RouteProfileID and ForwardingPolicyID of UPF
type RouteProfile struct {
	// Forwarding Policy ID of the route profile
	ForwardingPolicyID string `yaml:"forwardingPolicyID,omitempty" valid:"type(string),stringlength(1|255),required"`
}

func (r *RouteProfile) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(r)
	return result, appendInvalid(err)
}

// PfdContent represents the flow of the application
type PfdContent struct {
	// Identifies a PFD of an application identifier.
	PfdID string `yaml:"pfdID,omitempty" valid:"type(string),minstringlength(1),required"`
	// Represents a 3-tuple with protocol, server ip and server port for
	// UL/DL application traffic.
	FlowDescriptions []string `yaml:"flowDescriptions,omitempty" valid:"optional"`
	// Indicates a URL or a regular expression which is used to match the
	// significant parts of the URL.
	Urls []string `yaml:"urls,omitempty" valid:"optional"`
	// Indicates an FQDN or a regular expression as a domain name matching
	// criteria.
	DomainNames []string `yaml:"domainNames,omitempty" valid:"optional"`
}

func (p *PfdContent) Validate() (bool, error) {
	for _, flowDescription := range p.FlowDescriptions {
		if result := len(flowDescription) > 0; !result {
			err := errors.New("Invalid FlowDescription: " + flowDescription + ", should not be empty.")
			return false, err
		}
	}

	for _, url := range p.Urls {
		if result := govalidator.IsURL(url); !result {
			err := errors.New("Invalid Url: " + url + ", should be url.")
			return false, err
		}
	}

	for _, domainName := range p.DomainNames {
		if result := govalidator.IsDNSName(domainName); !result {
			err := errors.New("Invalid DomainName: " + domainName + ", should be domainName.")
			return false, err
		}
	}

	result, err := govalidator.ValidateStruct(p)
	return result, appendInvalid(err)
}

// PfdDataForApp represents the PFDs for an application identifier
type PfdDataForApp struct {
	// Identifier of an application.
	AppID string `yaml:"applicationId" valid:"type(string),minstringlength(1),required"`
	// PFDs for the application identifier.
	Pfds []PfdContent `yaml:"pfds" valid:"required"`
	// Caching time for an application identifier.
	CachingTime *time.Time `yaml:"cachingTime,omitempty" valid:"optional"`
}

func (p *PfdDataForApp) Validate() (bool, error) {
	for _, pfd := range p.Pfds {
		if result, err := pfd.Validate(); err != nil {
			return result, err
		}
	}

	result, err := govalidator.ValidateStruct(p)
	return result, appendInvalid(err)
}

type RoutingConfig struct {
	Info          *Info                        `yaml:"info" valid:"required"`
	UERoutingInfo map[string]UERoutingInfo     `yaml:"ueRoutingInfo" valid:"optional"`
	RouteProf     map[RouteProfID]RouteProfile `yaml:"routeProfile,omitempty" valid:"optional"`
	PfdDatas      []*PfdDataForApp             `yaml:"pfdDataForApp,omitempty" valid:"optional"`
	sync.RWMutex
}

func (r *RoutingConfig) Validate() (bool, error) {
	if info := r.Info; info != nil {
		if result, err := info.Validate(); err != nil {
			return result, err
		}
	}

	for _, ueRoutingInfo := range r.UERoutingInfo {
		if result, err := ueRoutingInfo.Validate(); err != nil {
			return result, err
		}
	}

	for _, routeProf := range r.RouteProf {
		if result, err := routeProf.Validate(); err != nil {
			return result, err
		}
	}

	for _, pfdData := range r.PfdDatas {
		if result, err := pfdData.Validate(); err != nil {
			return result, err
		}
	}

	result, err := govalidator.ValidateStruct(r)
	return result, appendInvalid(err)
}

// UserPlaneInformation describe core network userplane information
type UserPlaneInformation struct {
	UPNodes map[string]UPNodeConfigInterface `json:"upNodes" yaml:"upNodes" valid:"required"`
	Links   []*UPLink                        `json:"links" yaml:"links" valid:"required"`
}

func (u *UserPlaneInformation) Validate() (result bool, err error) {
	// register valid upNodeTypes to govalidator
	govalidator.TagMap["upNodeType"] = govalidator.Validator(func(str string) bool {
		return str == "AN" || str == "UPF"
	})

	// register valid interfaceTypes to govalidator
	govalidator.TagMap["interfaceType"] = govalidator.Validator(func(str string) bool {
		return str == "N3" || str == "N9"
	})

	// collect all validation errors for UserPlaneInformation
	var validationErrors govalidator.Errors
	result = true

	// validate struct field correctness
	if ok, errStruct := govalidator.ValidateStruct(u); !ok {
		result = false
		validationErrors = append(validationErrors, appendInvalid(errStruct).(govalidator.Errors)...)
	}

	var upNodeNames []string
	for name, upNode := range u.UPNodes {
		upNodeNames = append(upNodeNames, name)

		// call custom validation function (semantic validation)
		if ok, errSemantic := upNode.Validate(); !ok {
			result = false
			validationErrors = append(validationErrors, errSemantic.(govalidator.Errors)...)
		}
	}

	for _, link := range u.Links {
		if !slices.Contains(upNodeNames, link.A) || !slices.Contains(upNodeNames, link.B) {
			result = false
			validationErrors = append(validationErrors,
				fmt.Errorf("Link %s--%s contains unknown node name", link.A, link.B))
		}
	}

	return result, error(validationErrors)
}

// UPNode represent the user plane node
type UPNodeConfig struct {
	Type string `json:"type" yaml:"type" valid:"upNodeType,required"`
}

type UPNodeConfigInterface interface {
	Validate() (result bool, err error)
	GetType() string
}

type GNBConfig struct {
	*UPNodeConfig
}

type UPFConfig struct {
	*UPNodeConfig
	NodeID      string               `json:"nodeID" yaml:"nodeID" valid:"host,required"`
	SNssaiInfos []*SnssaiUpfInfoItem `json:"sNssaiUpfInfos" yaml:"sNssaiUpfInfos,omitempty" valid:"required"`
	Interfaces  []*Interface         `json:"interfaces" yaml:"interfaces" valid:"required"`
}

func (gNB *GNBConfig) Validate() (result bool, err error) {
	// no semantic validation (yet) for GNBs
	return true, nil
}

func (gNB *GNBConfig) GetType() string {
	return gNB.Type
}

func (upf *UPFConfig) Validate() (result bool, err error) {
	var validationErrors govalidator.Errors
	result = true

	n3IfsNum := 0
	n9IfsNum := 0
	for _, iface := range upf.Interfaces {
		if ok, errIface := iface.Validate(); !ok {
			result = false
			validationErrors = append(validationErrors, errIface.(govalidator.Errors)...)
		}

		if iface.InterfaceType == "N3" {
			n3IfsNum++
		}

		if iface.InterfaceType == "N9" {
			n9IfsNum++
		}
	}

	if n3IfsNum == 0 && n9IfsNum == 0 {
		result = false
		validationErrors = append(validationErrors,
			fmt.Errorf("UPF %s must have a user plane interface (N3 or N9)", upf.NodeID))
	}

	if n3IfsNum > 1 || n9IfsNum > 1 {
		result = false
		validationErrors = append(validationErrors,
			fmt.Errorf(
				"UPF %s: There is currently no support for multiple N3/ N9 interfaces: N3 number(%d), N9 number(%d)",
				upf.NodeID, n3IfsNum, n9IfsNum))
	}

	for _, snssaiInfo := range upf.SNssaiInfos {
		if ok, errSNSSAI := snssaiInfo.Validate(); !ok {
			result = false
			validationErrors = append(validationErrors, errSNSSAI.(govalidator.Errors)...)
		}
	}

	return result, error(validationErrors)
}

func (upf *UPFConfig) GetNodeID() string {
	return upf.NodeID
}

func (upf *UPFConfig) GetType() string {
	return upf.Type
}

type Interface struct {
	InterfaceType    models.UpInterfaceType `json:"interfaceType" yaml:"interfaceType" valid:"required"`
	Endpoints        []string               `json:"endpoints" yaml:"endpoints" valid:"required"`
	NetworkInstances []string               `json:"networkInstances" yaml:"networkInstances" valid:"optional"`
}

func (i *Interface) Validate() (result bool, err error) {
	var validationErrors govalidator.Errors
	result = true

	switch i.InterfaceType {
	case "N3":
	case "N9":
	case "N6":
	default:
		result = false
		validationErrors = append(validationErrors,
			fmt.Errorf("Invalid interface type %s: must be N3 or N9.", i.InterfaceType))
	}
	for _, endpoint := range i.Endpoints {
		if ok := govalidator.IsHost(endpoint); !ok {
			result = false
			validationErrors = append(validationErrors,
				fmt.Errorf("Invalid endpoint: %s should be one of IPv4, IPv6, FQDN.", endpoint))
		}
	}

	return result, error(validationErrors)
}

type SnssaiUpfInfoItem struct {
	SNssai         *models.Snssai    `json:"sNssai" yaml:"sNssai" valid:"required"`
	DnnUpfInfoList []*DnnUpfInfoItem `json:"dnnUpfInfoList" yaml:"dnnUpfInfoList" valid:"required"`
}

func (s *SnssaiUpfInfoItem) Validate() (result bool, err error) {
	var validationErrors govalidator.Errors
	result = true

	if s.SNssai != nil {
		if ok := (s.SNssai.Sst >= 0 && s.SNssai.Sst <= 255); !ok {
			result = false
			validationErrors = append(validationErrors,
				fmt.Errorf("Invalid sNssai.Sst: %s should be in range 0-255.",
					strconv.Itoa(int(s.SNssai.Sst))))
		}

		if s.SNssai.Sd != "" {
			if ok := govalidator.StringMatches(s.SNssai.Sd, "^[0-9A-Fa-f]{6}$"); !ok {
				result = false
				validationErrors = append(validationErrors,
					fmt.Errorf("Invalid sNssai.Sd: %s should be 3 bytes hex string and in range 000000-FFFFFF.",
						s.SNssai.Sd))
			}
		}
	}

	for _, dnnInfo := range s.DnnUpfInfoList {
		if ok, errDNNInfo := dnnInfo.Validate(); !ok {
			result = false
			validationErrors = append(validationErrors, errDNNInfo.(govalidator.Errors)...)
		}
	}

	if len(validationErrors) > 0 {
		return result, error(validationErrors)
	}

	return result, nil
}

type DnnUpfInfoItem struct {
	Dnn             string                  `json:"dnn" yaml:"dnn" valid:"required"`
	DnaiList        []string                `json:"dnaiList" yaml:"dnaiList" valid:"optional"`
	PduSessionTypes []models.PduSessionType `json:"pduSessionTypes" yaml:"pduSessionTypes" valid:"optional"`
	Pools           []*UEIPPool             `json:"pools" yaml:"pools" valid:"required"`
	StaticPools     []*UEIPPool             `json:"staticPools" yaml:"staticPools" valid:"optional"`
}

func (d *DnnUpfInfoItem) Validate() (result bool, err error) {
	// collect all errors
	var validationErrors govalidator.Errors
	result = true

	if len(d.Dnn) == 0 {
		result = false
		validationErrors = append(validationErrors,
			fmt.Errorf("Invalid DnnUpfInfoItem: dnn must not be empty."))
	}

	if len(d.Pools) == 0 {
		result = false
		validationErrors = append(validationErrors,
			fmt.Errorf("Invalid DnnUpfInfoItem: requires at least one dynamic IP pool."))
	}

	var prefixes []netaddr.IPPrefix
	for _, pool := range d.Pools {
		// CIDR check
		prefix, ok := netaddr.ParseIPPrefix(pool.Cidr)
		if ok != nil {
			result = false
			validationErrors = append(validationErrors, fmt.Errorf("Invalid CIDR: %s.", pool.Cidr))
		} else {
			prefixes = append(prefixes, prefix)
		}
	}

	// check overlap within dynamic pools
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if prefixes[i].Overlaps(prefixes[j]) {
				result = false
				validationErrors = append(validationErrors,
					fmt.Errorf("overlap detected between dynamic pools %s and %s", prefixes[i], prefixes[j]))
			}
		}
	}

	// check static pools CIDR and overlap with dynamic pools
	var staticPrefixes []netaddr.IPPrefix
	for _, staticPool := range d.StaticPools {
		// CIDR check
		staticPrefix, ok := netaddr.ParseIPPrefix(staticPool.Cidr)
		if ok != nil {
			result = false
			validationErrors = append(validationErrors, fmt.Errorf("Invalid CIDR: %s.", staticPool.Cidr))
		} else {
			staticPrefixes = append(staticPrefixes, staticPrefix)
			for _, prefix := range prefixes {
				if staticPrefix.Overlaps(prefix) {
					result = false
					validationErrors = append(validationErrors,
						fmt.Errorf("overlap detected between static pool %s and dynamic pool %s", staticPrefix, prefix))
				}
			}
		}
	}

	// check overlap within static pools
	for i := 0; i < len(staticPrefixes); i++ {
		for j := i + 1; j < len(staticPrefixes); j++ {
			if staticPrefixes[i].Overlaps(staticPrefixes[j]) {
				result = false
				validationErrors = append(validationErrors,
					fmt.Errorf("overlap detected between static pools %s and %s", staticPrefixes[i], staticPrefixes[j]))
			}
		}
	}

	return result, error(validationErrors)
}

type UPLink struct {
	A string `json:"A" yaml:"A" valid:"required"`
	B string `json:"B" yaml:"B" valid:"required"`
}

type UEIPPool struct {
	Cidr string `yaml:"cidr" valid:"cidr,required"`
}

type SpecificPath struct {
	DestinationIP   string   `yaml:"dest,omitempty" valid:"cidr,required"`
	DestinationPort string   `yaml:"DestinationPort,omitempty" valid:"port,optional"`
	Path            []string `yaml:"path" valid:"required"`
}

func (p *SpecificPath) Validate() (bool, error) {
	govalidator.TagMap["cidr"] = govalidator.Validator(func(str string) bool {
		isCIDR := govalidator.IsCIDR(str)
		return isCIDR
	})

	for _, upf := range p.Path {
		if result := len(upf); result == 0 {
			err := errors.New("Invalid UPF: " + upf + ", should not be empty")
			return false, err
		}
	}

	result, err := govalidator.ValidateStruct(p)
	return result, appendInvalid(err)
}

type PlmnID struct {
	Mcc string `yaml:"mcc"`
	Mnc string `yaml:"mnc"`
}

func (p *PlmnID) Validate() (bool, error) {
	mcc := p.Mcc
	if result := govalidator.StringMatches(mcc, "^[0-9]{3}$"); !result {
		err := fmt.Errorf("Invalid mcc: %s, should be a 3-digit number", mcc)
		return false, err
	}

	mnc := p.Mnc
	if result := govalidator.StringMatches(mnc, "^[0-9]{2,3}$"); !result {
		err := fmt.Errorf("Invalid mnc: %s, should be a 2 or 3-digit number", mnc)
		return false, err
	}
	return true, nil
}

type TimerValue struct {
	Enable        bool          `yaml:"enable" valid:"type(bool)"`
	ExpireTime    time.Duration `yaml:"expireTime" valid:"type(time.Duration)"`
	MaxRetryTimes int           `yaml:"maxRetryTimes,omitempty" valid:"type(int)"`
}

func (t *TimerValue) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(t)
	return result, err
}

func (c *Config) GetVersion() string {
	c.RLock()
	defer c.RUnlock()

	if c.Info.Version != "" {
		return c.Info.Version
	}
	return ""
}

func (r *RoutingConfig) GetVersion() string {
	r.RLock()
	defer r.RUnlock()

	if r.Info != nil && r.Info.Version != "" {
		return r.Info.Version
	}
	return ""
}

func (c *Config) SetLogEnable(enable bool) {
	c.Lock()
	defer c.Unlock()

	if c.Logger == nil {
		logger.CfgLog.Warnf("Logger should not be nil")
		c.Logger = &Logger{
			Enable: enable,
			Level:  "info",
		}
	} else {
		c.Logger.Enable = enable
	}
}

func (c *Config) SetLogLevel(level string) {
	c.Lock()
	defer c.Unlock()

	if c.Logger == nil {
		logger.CfgLog.Warnf("Logger should not be nil")
		c.Logger = &Logger{
			Level: level,
		}
	} else {
		c.Logger.Level = level
	}
}

func (c *Config) SetLogReportCaller(reportCaller bool) {
	c.Lock()
	defer c.Unlock()

	if c.Logger == nil {
		logger.CfgLog.Warnf("Logger should not be nil")
		c.Logger = &Logger{
			Level:        "info",
			ReportCaller: reportCaller,
		}
	} else {
		c.Logger.ReportCaller = reportCaller
	}
}

func (c *Config) GetLogEnable() bool {
	c.RLock()
	defer c.RUnlock()
	if c.Logger == nil {
		logger.CfgLog.Warnf("Logger should not be nil")
		return false
	}
	return c.Logger.Enable
}

func (c *Config) GetLogLevel() string {
	c.RLock()
	defer c.RUnlock()
	if c.Logger == nil {
		logger.CfgLog.Warnf("Logger should not be nil")
		return "info"
	}
	return c.Logger.Level
}

func (c *Config) GetLogReportCaller() bool {
	c.RLock()
	defer c.RUnlock()
	if c.Logger == nil {
		logger.CfgLog.Warnf("Logger should not be nil")
		return false
	}
	return c.Logger.ReportCaller
}

func (c *Config) GetSbiScheme() string {
	c.RLock()
	defer c.RUnlock()
	if c.Configuration != nil && c.Configuration.Sbi != nil && c.Configuration.Sbi.Scheme != "" {
		return c.Configuration.Sbi.Scheme
	}
	return SmfSbiDefaultScheme
}

func (c *Config) GetCertPemPath() string {
	c.RLock()
	defer c.RUnlock()
	return c.Configuration.Sbi.Tls.Pem
}

func (c *Config) GetCertKeyPath() string {
	c.RLock()
	defer c.RUnlock()
	return c.Configuration.Sbi.Tls.Key
}

func appendInvalid(err error) error {
	var errs govalidator.Errors

	if err == nil {
		return nil
	}

	es := err.(govalidator.Errors).Errors()
	for _, e := range es {
		errs = append(errs, fmt.Errorf("invalid %w", e))
	}

	return error(errs)
}
