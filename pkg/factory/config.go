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
	Info          *Info          `yaml:"info" valid:"required,configValidator"`
	Configuration *Configuration `yaml:"configuration" valid:"required"`
	Logger        *Logger        `yaml:"logger" valid:"required"`
	sync.RWMutex
}

func (c *Config) Validate() (bool, error) {
	// register custom tag validators
	govalidator.TagMap["scheme"] = govalidator.Validator(func(str string) bool {
		return str == "https" || str == "http"
	})

	// register custom semantic validators
	govalidator.CustomTypeTagMap.Set("snssaiValidator", func(i interface{}, context interface{}) bool {
		// context == struct this field is in
		// i == the validated field
		sNssai := i.(models.Snssai)
		ok, _ := ValidateSNssai(&sNssai)
		return ok
	})

	govalidator.CustomTypeTagMap.Set("upNodeValidator", func(i interface{}, context interface{}) bool {
		upNode := i.(UPNodeConfigInterface)
		ok, _ := upNode.Validate()
		return ok
	})

	govalidator.CustomTypeTagMap.Set("poolValidator", func(i interface{}, context interface{}) bool {
		dnnUpfInfoItem := context.(*DnnUpfInfoItem)
		ok, _ := ValidateUEIPPools(dnnUpfInfoItem.Pools, dnnUpfInfoItem.StaticPools)
		return ok
	})

	govalidator.CustomTypeTagMap.Set("linkValidator", func(i interface{}, context interface{}) bool {
		link := i.(UPLink)
		var upNodeNames []string
		for name, _ := range context.(UserPlaneInformation).UPNodes {
			upNodeNames = append(upNodeNames, name)
		}
		ok, _ := ValidateLink(&link, upNodeNames)
		return ok
	})

	govalidator.CustomTypeTagMap.Set("pathValidator", func(i interface{}, context interface{}) bool {
		path := context.([]string)
		ok, _ := ValidatePath(path)
		return ok
	})

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

type Configuration struct {
	SmfName string `yaml:"smfName" valid:"type(string),required"`
	// done
	Sbi *Sbi `yaml:"sbi" valid:"required"`
	// done
	PFCP *PFCP `yaml:"pfcp" valid:"required"`
	// done
	NrfUri               string               `yaml:"nrfUri" valid:"url,required"`
	NrfCertPem           string               `yaml:"nrfCertPem,omitempty" valid:"optional"`
	UserPlaneInformation UserPlaneInformation `yaml:"userplaneInformation" valid:"required"`
	// done
	ServiceNameList []string `yaml:"serviceNameList" valid:"required,in(nsmf-pdusession,nsmf-event-exposure,nsmf-oam)"`
	// done
	SNssaiInfo         []*SnssaiInfoItem `yaml:"snssaiInfos" valid:"required"`
	ULCL               bool              `yaml:"ulcl" valid:"type(bool),optional"`
	PLMNList           []PlmnID          `yaml:"plmnList"  valid:"optional"`
	Locality           string            `yaml:"locality" valid:"type(string),optional"`
	UrrPeriod          uint16            `yaml:"urrPeriod,omitempty" valid:"optional"`
	UrrThreshold       uint64            `yaml:"urrThreshold,omitempty" valid:"optional"`
	T3591              *TimerValue       `yaml:"t3591" valid:"required"`
	T3592              *TimerValue       `yaml:"t3592" valid:"required"`
	NwInstFqdnEncoding bool              `yaml:"nwInstFqdnEncoding" valid:"type(bool),optional"`
	RequestedUnit      int32             `yaml:"requestedUnit,omitempty" valid:"optional"`
}

type Logger struct {
	Enable       bool   `yaml:"enable" valid:"type(bool)"`
	Level        string `yaml:"level" valid:"required,in(trace|debug|info|warn|error|fatal|panic)"`
	ReportCaller bool   `yaml:"reportCaller" valid:"type(bool)"`
}

type SnssaiInfoItem struct {
	SNssai   *models.Snssai       `yaml:"sNssai" valid:"required,snssaiValidator"`
	DnnInfos []*SnssaiDnnInfoItem `yaml:"dnnInfos" valid:"required"`
}

func ValidateSNssai(sNssai *models.Snssai) (bool, error) {
	if result := (sNssai.Sst >= 0 && sNssai.Sst <= 255); !result {
		err := errors.New("Invalid sNssai.Sst: " + strconv.Itoa(int(sNssai.Sst)) + ", should be in range 0~255.")
		return false, err
	}

	if sNssai.Sd != "" {
		if result := govalidator.StringMatches(sNssai.Sd, "^[0-9A-Fa-f]{6}$"); !result {
			err := errors.New("Invalid sNssai.Sd: " + sNssai.Sd +
				", should be 3 bytes hex string and in range 000000~FFFFFF.")
			return false, err
		}
	}
	return true, nil
}

type SnssaiDnnInfoItem struct {
	Dnn   string `yaml:"dnn" valid:"type(string),minstringlength(1),required"`
	DNS   *DNS   `yaml:"dns" valid:"required"`
	PCSCF *PCSCF `yaml:"pcscf,omitempty" valid:"optional"`
}

type Sbi struct {
	Scheme string `yaml:"scheme" valid:"scheme,required"`
	//done
	Tls          *Tls   `yaml:"tls" valid:"optional"`
	RegisterIPv4 string `yaml:"registerIPv4,omitempty" valid:"host,optional"` // IP that is registered at NRF.
	// IPv6Addr string `yaml:"ipv6Addr,omitempty"`
	BindingIPv4 string `yaml:"bindingIPv4,omitempty" valid:"host,required"` // IP used to run the server in the node.
	Port        int    `yaml:"port,omitempty" valid:"port,optional"`
}

type Tls struct {
	Pem string `yaml:"pem,omitempty" valid:"type(string),minstringlength(1),required"`
	Key string `yaml:"key,omitempty" valid:"type(string),minstringlength(1),required"`
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

type DNS struct {
	IPv4Addr string `yaml:"ipv4,omitempty" valid:"ipv4,required"`
	IPv6Addr string `yaml:"ipv6,omitempty" valid:"ipv6,optional"`
}

type PCSCF struct {
	IPv4Addr string `yaml:"ipv4,omitempty" valid:"ipv4,required"`
}

type Path struct {
	DestinationIP   string   `yaml:"DestinationIP,omitempty" valid:"ipv4,required"`
	DestinationPort string   `yaml:"DestinationPort,omitempty" valid:"port,optional"`
	UPF             []string `yaml:"UPF,omitempty" valid:"required"`
}

type UERoutingInfo struct {
	Members       []string       `yaml:"members" valid:"required,matches(imsi-[0-9]{5,15}$)-Invalid member (SUPI)"`
	AN            string         `yaml:"AN,omitempty" valid:"ipv4,optional"`
	PathList      []Path         `yaml:"PathList,omitempty" valid:"optional"`
	Topology      []UPLink       `yaml:"topology" valid:"required"` // TODO: validation with topology names
	SpecificPaths []SpecificPath `yaml:"specificPath,omitempty" valid:"optional"`
}

// RouteProfID is string providing a Route Profile identifier.
type RouteProfID string

// RouteProfile maintains the mapping between RouteProfileID and ForwardingPolicyID of UPF
type RouteProfile struct {
	// Forwarding Policy ID of the route profile
	ForwardingPolicyID string `yaml:"forwardingPolicyID,omitempty" valid:"type(string),stringlength(1|255),required"`
}

// PfdContent represents the flow of the application
type PfdContent struct {
	// Identifies a PFD of an application identifier.
	PfdID string `yaml:"pfdID,omitempty" valid:"type(string),minstringlength(1),required"`
	// Represents a 3-tuple with protocol, server ip and server port for
	// UL/DL application traffic.
	FlowDescriptions []string `yaml:"flowDescriptions,omitempty" valid:"minstringlen(1),optional"`
	// Indicates a URL or a regular expression which is used to match the
	// significant parts of the URL.
	Urls []string `yaml:"urls,omitempty" valid:"url,optional"`
	// Indicates an FQDN or a regular expression as a domain name matching
	// criteria.
	DomainNames []string `yaml:"domainNames,omitempty" valid:"dns,optional"`
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

type RoutingConfig struct {
	Info          *Info                        `yaml:"info" valid:"required"`
	UERoutingInfo map[string]UERoutingInfo     `yaml:"ueRoutingInfo" valid:"optional"`
	RouteProf     map[RouteProfID]RouteProfile `yaml:"routeProfile,omitempty" valid:"optional"`
	PfdDatas      []*PfdDataForApp             `yaml:"pfdDataForApp,omitempty" valid:"optional"`
	sync.RWMutex
}

func (r *RoutingConfig) Validate() (bool, error) {
	result, err := govalidator.ValidateStruct(r)
	return result, appendInvalid(err)
}

// UserPlaneInformation describe core network userplane information
type UserPlaneInformation struct {
	UPNodes map[string]*UPNodeConfigInterface `json:"upNodes" yaml:"upNodes" valid:"required,upNodeValidator"`
	Links   []*UPLink                         `json:"links" yaml:"links" valid:"required,linkValidator"`
}

// UPNode represent the user plane node
type UPNodeConfig struct {
	Type string `json:"type" yaml:"type" valid:"required,in(AN,UPF)"`
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
	SNssaiInfos []*SnssaiUpfInfoItem `json:"sNssaiUpfInfos" yaml:"sNssaiUpfInfos" valid:"required"`
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
	n3IfsNum := 0
	n9IfsNum := 0
	for _, iface := range upf.Interfaces {
		if iface.InterfaceType == "N3" {
			n3IfsNum++
		}

		if iface.InterfaceType == "N9" {
			n9IfsNum++
		}
	}

	if n3IfsNum == 0 && n9IfsNum == 0 {
		return false, fmt.Errorf("UPF %s must have a user plane interface (N3 or N9)", upf.NodeID)
	}

	if n3IfsNum > 1 || n9IfsNum > 1 {
		return false, fmt.Errorf(
			"UPF %s: There is currently no support for multiple N3/ N9 interfaces: N3 number(%d), N9 number(%d)",
			upf.NodeID, n3IfsNum, n9IfsNum)
	}

	return true, nil
}

func (upf *UPFConfig) GetNodeID() string {
	return upf.NodeID
}

func (upf *UPFConfig) GetType() string {
	return upf.Type
}

type Interface struct {
	InterfaceType    models.UpInterfaceType `json:"interfaceType" yaml:"interfaceType" valid:"required,in(N3,N9)"`
	Endpoints        []string               `json:"endpoints" yaml:"endpoints" valid:"host,required"`
	NetworkInstances []string               `json:"networkInstances" yaml:"networkInstances" valid:"optional"`
}

type SnssaiUpfInfoItem struct {
	SNssai         *models.Snssai    `json:"sNssai" yaml:"sNssai" valid:"snssaiValidator,required"`
	DnnUpfInfoList []*DnnUpfInfoItem `json:"dnnUpfInfoList" yaml:"dnnUpfInfoList" valid:"required"`
}

type DnnUpfInfoItem struct {
	Dnn             string                  `json:"dnn" yaml:"dnn" valid:"minstringlength(1),required"`
	DnaiList        []string                `json:"dnaiList" yaml:"dnaiList" valid:"optional"`
	PduSessionTypes []models.PduSessionType `json:"pduSessionTypes" yaml:"pduSessionTypes" valid:"optional,in(IPv4,IPv6,IPV4V6,UNSTRUCTURED,ETHERNET)"`
	Pools           []*UEIPPool             `json:"pools" yaml:"pools" valid:"optional,poolValidator"`
	StaticPools     []*UEIPPool             `json:"staticPools" yaml:"staticPools" valid:"optional,poolValidator"`
}

func ValidateUEIPPools(dynamic []*UEIPPool, static []*UEIPPool) (bool, error) {
	var prefixes []netaddr.IPPrefix
	for _, pool := range dynamic {
		// CIDR check
		prefix, ok := netaddr.ParseIPPrefix(pool.Cidr)
		if ok != nil {
			return false, fmt.Errorf("Invalid pool CIDR: %s.", pool.Cidr)
		} else {
			prefixes = append(prefixes, prefix)
		}
	}

	// check overlap within dynamic pools
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if prefixes[i].Overlaps(prefixes[j]) {
				return false, fmt.Errorf("overlap detected between dynamic pools %s and %s", prefixes[i], prefixes[j])
			}
		}
	}

	// check static pools CIDR and overlap with dynamic pools
	var staticPrefixes []netaddr.IPPrefix
	for _, staticPool := range static {
		// CIDR check
		staticPrefix, ok := netaddr.ParseIPPrefix(staticPool.Cidr)
		if ok != nil {
			return false, fmt.Errorf("Invalid CIDR: %s.", staticPool.Cidr)
		} else {
			staticPrefixes = append(staticPrefixes, staticPrefix)
			for _, prefix := range prefixes {
				if staticPrefix.Overlaps(prefix) {
					return false, fmt.Errorf("overlap detected between static pool %s and dynamic pool %s", staticPrefix, prefix)
				}
			}
		}
	}

	// check overlap within static pools
	for i := 0; i < len(staticPrefixes); i++ {
		for j := i + 1; j < len(staticPrefixes); j++ {
			if staticPrefixes[i].Overlaps(staticPrefixes[j]) {
				return false, fmt.Errorf("overlap detected between static pools %s and %s", staticPrefixes[i], staticPrefixes[j])
			}
		}
	}

	return true, nil
}

type UPLink struct {
	A string `json:"A" yaml:"A" valid:"required"`
	B string `json:"B" yaml:"B" valid:"required"`
}

func ValidateLink(link *UPLink, upNodeNames []string) (bool, error) {
	if ok := !slices.Contains(upNodeNames, link.A) || !slices.Contains(upNodeNames, link.B); !ok {
		return false, fmt.Errorf("Link %s--%s contains unknown node name", link.A, link.B)
	}
	return true, nil
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

type UEIPPool struct {
	Cidr string `yaml:"cidr" valid:"cidr,required"`
}

type SpecificPath struct {
	DestinationIP   string   `yaml:"dest,omitempty" valid:"cidr,required"`
	DestinationPort string   `yaml:"DestinationPort,omitempty" valid:"port,optional"`
	Path            []string `yaml:"path" valid:"pathValidator,required"`
}

func ValidatePath(path []string) (bool, error) {
	for _, upf := range path {
		if result := len(upf); result == 0 {
			err := errors.New("Invalid UPF: " + upf + ", should not be empty")
			return false, err
		}
	}

	return true, nil
}

type PlmnID struct {
	Mcc string `yaml:"mcc" valid:"matches(^[0-9]{3}$),required"`
	Mnc string `yaml:"mnc" valid:"matches(^[0-9]{2,3}$),required"`
}

type TimerValue struct {
	Enable        bool          `yaml:"enable" valid:"type(bool)"`
	ExpireTime    time.Duration `yaml:"expireTime" valid:"type(time.Duration)"`
	MaxRetryTimes int           `yaml:"maxRetryTimes,omitempty" valid:"type(int)"`
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
