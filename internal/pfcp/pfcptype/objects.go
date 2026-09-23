// Package pfcptype contains SMF-owned PFCP domain values.
//
// The types in this package are intentionally independent from the wire
// representation in github.com/wmnsk/go-pfcp. Conversion to and from IE values
// belongs at the PFCP build/parse boundary. ReportingTrigger.IE is the sole
// exception because its domain value is already a protocol bit set.
package pfcptype

import (
	"context"
	"encoding/binary"
	"net"

	"github.com/wmnsk/go-pfcp/ie"
)

const (
	NodeIdTypeIpv4Address uint8 = iota
	NodeIdTypeIpv6Address
	NodeIdTypeFqdn
)

type NodeID struct {
	NodeIdType uint8
	IP         net.IP
	FQDN       string
}

func (n NodeID) String() string {
	switch n.NodeIdType {
	case NodeIdTypeIpv4Address, NodeIdTypeIpv6Address:
		return n.IP.String()
	case NodeIdTypeFqdn:
		return n.FQDN
	default:
		return ""
	}
}

// ResolveNodeIdToIp returns the configured address or resolves an FQDN.
// Lookup failure returns an unspecified IPv4 address.
func (n NodeID) ResolveNodeIdToIp() net.IP {
	switch n.NodeIdType {
	case NodeIdTypeIpv4Address, NodeIdTypeIpv6Address:
		return n.IP
	case NodeIdTypeFqdn:
		addresses, err := net.DefaultResolver.LookupIPAddr(context.Background(), n.FQDN)
		if err != nil || len(addresses) == 0 {
			return net.IPv4zero
		}
		return addresses[0].IP
	default:
		return net.IPv4zero
	}
}

func (n *NodeID) EqualsTo(other *NodeID) bool {
	if n == nil || other == nil || n.NodeIdType != other.NodeIdType {
		return false
	}

	switch n.NodeIdType {
	case NodeIdTypeIpv4Address, NodeIdTypeIpv6Address:
		return n.IP.Equal(other.IP)
	case NodeIdTypeFqdn:
		return n.FQDN == other.FQDN
	default:
		return false
	}
}

const (
	OuterHeaderRemovalGtpUUdpIpv4 uint8 = iota
	OuterHeaderRemovalGtpUUdpIpv6
	OuterHeaderRemovalUdpIpv4
	OuterHeaderRemovalUdpIpv6
)

type OuterHeaderRemoval struct {
	OuterHeaderRemovalDescription uint8
}

type FTEID struct {
	Chid        bool
	Ch          bool
	V6          bool
	V4          bool
	Teid        uint32
	Ipv4Address net.IP
	Ipv6Address net.IP
	ChooseId    uint8
}

const (
	SourceInterfaceAccess uint8 = iota
	SourceInterfaceCore
	SourceInterfaceSgiLanN6Lan
	SourceInterfaceCpFunction
	SourceInterface5GVNInternal
)

type SourceInterface struct {
	InterfaceValue uint8
}

type NetworkInstance struct {
	FQDNEncoding    bool
	NetworkInstance string
}

type UEIPAddress struct {
	Ipv6d                    bool
	Sd                       bool
	V4                       bool
	V6                       bool
	Ipv4Address              net.IP
	Ipv6Address              net.IP
	Ipv6PrefixDelegationBits uint8
}

type EthernetPacketFilter struct {
	MacAddresses []string
	Ethertype    uint16
	CTag         *VLANTag
	STag         *VLANTag
	SDFFilter    []SDFFilter
}

type VLANTag struct {
	VID uint16
	DEI bool
	PCP uint8
}

type SDFFilter struct {
	Bid                     bool
	Fl                      bool
	Spi                     bool
	Ttc                     bool
	Fd                      bool
	LengthOfFlowDescription uint16
	FlowDescription         []byte
	TosTrafficClass         []byte
	SecurityParameterIndex  []byte
	FlowLabel               []byte
	SdfFilterId             uint32
}

type ApplyAction struct {
	Drop bool
	Forw bool
	Buff bool
	Nocp bool
	Dupl bool
	Ipma bool
	Ipmd bool
	Dfrt bool
	Edrt bool
	Bdpn bool
	Ddpn bool
	Fssm bool
	Mbsu bool
}

const (
	DestinationInterfaceAccess uint8 = iota
	DestinationInterfaceCore
	DestinationInterfaceSgiLanN6Lan
	DestinationInterfaceCpFunction
	DestinationInterfaceLiFunction
	DestinationInterface5GVNInternal
)

type DestinationInterface struct {
	InterfaceValue uint8
}

const (
	OuterHeaderCreationGtpUUdpIpv4 uint16 = 1 << (8 + iota)
	OuterHeaderCreationGtpUUdpIpv6
	OuterHeaderCreationUdpIpv4
	OuterHeaderCreationUdpIpv6
)

type OuterHeaderCreation struct {
	OuterHeaderCreationDescription uint16
	Teid                           uint32
	Ipv4Address                    net.IP
	Ipv6Address                    net.IP
	PortNumber                     uint16
}

type DownlinkDataNotificationDelay struct {
	DelayValue uint8
}

type SuggestedBufferingPacketsCount struct {
	PacketCountValue uint8
}

type QFI struct {
	QFI uint8
}

type GateStatusType uint8

const (
	GateOpen GateStatusType = iota
	GateClose
)

type GateStatus struct {
	ULGate GateStatusType
	DLGate GateStatusType
}

type MBR struct {
	ULMBR uint64
	DLMBR uint64
}

type GBR struct {
	ULGBR uint64
	DLGBR uint64
}

const (
	PDNTypeIpv4 uint8 = iota + 1
	PDNTypeIpv6
	PDNTypeIpv4v6
	PDNTypeNonIp
	PDNTypeEthernet
)

type PDNType struct {
	PdnType uint8
}

const (
	RPT_TRIG_PERIO uint32 = 1 << iota
	RPT_TRIG_VOLTH
	RPT_TRIG_TIMTH
	RPT_TRIG_QUHTI
	RPT_TRIG_START
	RPT_TRIG_STOPT
	RPT_TRIG_DROTH
	RPT_TRIG_LIUSA
	RPT_TRIG_VOLQU
	RPT_TRIG_TIMQU
	RPT_TRIG_ENVCL
	RPT_TRIG_MACAR
	RPT_TRIG_EVETH
	RPT_TRIG_EVEQU
	RPT_TRIG_IPMJL
	RPT_TRIG_QUVTI
	RPT_TRIG_REEMR
	RPT_TRIG_UPINT
)

type ReportingTrigger struct {
	Flags uint32
}

func (r ReportingTrigger) IE() *ie.IE {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, r.Flags)
	if b[2] != 0 {
		return ie.NewReportingTriggers(b[:3]...)
	}

	// go-pfcp requires the two-octet form used by the R15 Reporting Triggers IE.
	return ie.NewReportingTriggers(b[:2]...)
}

func (r *ReportingTrigger) SetPERIO() { r.Flags |= RPT_TRIG_PERIO }
func (r *ReportingTrigger) SetVOLTH() { r.Flags |= RPT_TRIG_VOLTH }
func (r *ReportingTrigger) SetTIMTH() { r.Flags |= RPT_TRIG_TIMTH }
func (r *ReportingTrigger) SetQUHTI() { r.Flags |= RPT_TRIG_QUHTI }
func (r *ReportingTrigger) SetSTART() { r.Flags |= RPT_TRIG_START }
func (r *ReportingTrigger) SetSTOPT() { r.Flags |= RPT_TRIG_STOPT }
func (r *ReportingTrigger) SetDROTH() { r.Flags |= RPT_TRIG_DROTH }
func (r *ReportingTrigger) SetLIUSA() { r.Flags |= RPT_TRIG_LIUSA }
func (r *ReportingTrigger) SetVOLQU() { r.Flags |= RPT_TRIG_VOLQU }
func (r *ReportingTrigger) SetTIMQU() { r.Flags |= RPT_TRIG_TIMQU }
func (r *ReportingTrigger) SetENVCL() { r.Flags |= RPT_TRIG_ENVCL }
func (r *ReportingTrigger) SetMACAR() { r.Flags |= RPT_TRIG_MACAR }
func (r *ReportingTrigger) SetEVETH() { r.Flags |= RPT_TRIG_EVETH }
func (r *ReportingTrigger) SetEVEQU() { r.Flags |= RPT_TRIG_EVEQU }
func (r *ReportingTrigger) SetIPMJL() { r.Flags |= RPT_TRIG_IPMJL }
func (r *ReportingTrigger) SetQUVTI() { r.Flags |= RPT_TRIG_QUVTI }
func (r *ReportingTrigger) SetREEMR() { r.Flags |= RPT_TRIG_REEMR }
func (r *ReportingTrigger) SetUPINT() { r.Flags |= RPT_TRIG_UPINT }

func (r ReportingTrigger) HasVOLTH() bool { return r.Flags&RPT_TRIG_VOLTH != 0 }
func (r ReportingTrigger) HasVOLQU() bool { return r.Flags&RPT_TRIG_VOLQU != 0 }
func (r ReportingTrigger) HasQUVTI() bool { return r.Flags&RPT_TRIG_QUVTI != 0 }
func (r ReportingTrigger) HasSTART() bool { return r.Flags&RPT_TRIG_START != 0 }

const (
	MeasureInfoMBQE = 0x01
	MeasureInfoMNOP = 0x10
)

type MeasurementInformation struct {
	Flags uint8
}

func (m MeasurementInformation) HasMNOP() bool { return m.Flags&MeasureInfoMNOP != 0 }
func (m MeasurementInformation) HasMBQE() bool { return m.Flags&MeasureInfoMBQE != 0 }
