package smf_context

import (
	"gofree5gc/lib/pfcp/pfcpType"
	"net"
)

type UserPlaneInformation struct {
	UPNodes       map[string]*UPNode
	UPFs          map[string]*UPNode
	AccessNetwork map[string]*UPNode
}

type UPNodeType string

const (
	UPNODE_UPF UPNodeType = "UPF"
	UPNODE_AN  UPNodeType = "AN"
)

// UPNode represent the user plane node
type UPNode struct {
	Type           UPNodeType
	NodeID         pfcpType.NodeID
	UPResourceIP   net.IP
	ANIP           net.IP
	Dnn            string
	Links          []*UPNode
	UPFInformation *UPFInformation
}
