package smf_context

import "github.com/google/uuid"

type DataPathNode struct {
	UPF  *UPF
	Prev *DataPathLink
	Next map[uuid.UUID]*DataPathLink
}

type DataPathLink struct {
	To *DataPathNode

	// Filter Rules
	DestinationIP   string
	DestinationPort string

	// related context
	PDR *PDR
}
