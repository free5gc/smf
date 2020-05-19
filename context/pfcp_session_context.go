package context

import (
	"free5gc/lib/pfcp/pfcpType"
)

type PFCPSessionContext struct {
	PDRs       map[uint16]*PDR
	NodeID     pfcpType.NodeID
	LocalSEID  uint64
	RemoteSEID uint64
}
