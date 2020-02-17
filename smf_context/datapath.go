package smf_context

type DataPathNode struct {
	UPF  *UPF
	Prev *DataPathLink
	Next map[string]*DataPathLink //uuid to DataPathLink

	//for UE Routing Topology
	IsBranchingPoint bool
}

type DataPathLink struct {
	To *DataPathNode

	// Filter Rules
	DestinationIP   string
	DestinationPort string

	// related context
	PDR *PDR
}
