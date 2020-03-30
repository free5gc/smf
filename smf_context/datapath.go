package smf_context

import (
	"fmt"
)

// GTPTunnel represents the GTP tunnel information
type GTPTunnel struct {
	SrcEndPoint  *DataPathNode
	DestEndPoint *DataPathNode

	TEID       uint32
	MatchedPDR *PDR
}

type DataPathNode struct {
	UPF          *UPF
	DataPathToAN *DataPathDownLink
	DataPathToDN map[string]*DataPathUpLink //uuid to DataPathLink

	UpLinkTunnel   *GTPTunnel
	DownLinkTunnel *GTPTunnel
	//for UE Routing Topology
	//for special case:
	//branching & leafnode

	InUse                bool
	IsBranchingPoint     bool
	DLDataPathLinkForPSA *DataPathUpLink
	BPUpLinkPDRs         map[string]*DataPathDownLink // uuid to UpLink

	HaveSession bool
}

type DataPathDownLink struct {
	To *DataPathNode

	// Filter Rules
	DestinationIP   string
	DestinationPort string

	// related context
	UpLinkPDR *PDR
}

type DataPathUpLink struct {
	To *DataPathNode

	// Filter Rules
	DestinationIP   string
	DestinationPort string

	// related context
	DownLinkPDR *PDR
}

func NewDataPathNode() (node *DataPathNode) {

	node = &DataPathNode{
		UPF:                  nil,
		DataPathToDN:         make(map[string]*DataPathUpLink),
		DataPathToAN:         NewDataPathDownLink(),
		IsBranchingPoint:     false,
		DLDataPathLinkForPSA: nil,
		BPUpLinkPDRs:         make(map[string]*DataPathDownLink),
	}
	return
}

func NewDataPathDownLink() (link *DataPathDownLink) {

	link = &DataPathDownLink{
		To:              nil,
		DestinationIP:   "",
		DestinationPort: "",
		UpLinkPDR:       nil,
	}
	return
}

func NewDataPathUpLink() (link *DataPathUpLink) {

	link = &DataPathUpLink{
		To:              nil,
		DestinationIP:   "",
		DestinationPort: "",
		DownLinkPDR:     nil,
	}
	return
}

func (node *DataPathNode) SetUpLinkSrcNode(nextUpLinkNode *DataPathNode) (err error) {

	node.UpLinkTunnel = new(GTPTunnel)
	node.UpLinkTunnel.SrcEndPoint = nextUpLinkNode
	node.UpLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	node.UpLinkTunnel.MatchedPDR, _ = destUPF.AddPDR()

	teid, _ := destUPF.GenerateTEID()
	node.UpLinkTunnel.TEID = teid
	return
}

func (node *DataPathNode) SetDownLinkSrcNode(nextDownLinkNode *DataPathNode) (err error) {

	node.DownLinkTunnel = new(GTPTunnel)
	node.DownLinkTunnel.SrcEndPoint = nextDownLinkNode
	node.DownLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	node.DownLinkTunnel.MatchedPDR, _ = destUPF.AddPDR()

	teid, _ := destUPF.GenerateTEID()
	node.DownLinkTunnel.TEID = teid

	return
}

func (node *DataPathNode) AddDestinationOfChild(child *DataPathNode, Dest *DataPathUpLink) (err error) {

	child_id, err := child.GetUPFID()

	if err != nil {
		return err
	}
	if child_link, exist := node.DataPathToDN[child_id]; exist {

		child_link.DestinationIP = Dest.DestinationIP
		child_link.DestinationPort = Dest.DestinationPort

	}

	return
}

func (node *DataPathNode) GetUPFID() (id string, err error) {
	node_ip := node.GetNodeIP()
	var exist bool

	if id, exist = smfContext.UserPlaneInformation.UPFsIPtoID[node_ip]; !exist {
		err = fmt.Errorf("UPNode IP %s doesn't exist in smfcfg.conf, please sync the config files!", node_ip)
		return "", err
	}

	return id, nil

}

func (node *DataPathNode) GetNodeIP() (ip string) {

	ip = node.UPF.NodeID.ResolveNodeIdToIp().String()
	return
}

func (node *DataPathNode) IsANUPF() bool {

	if node.DataPathToAN.To == nil {
		return true
	} else {
		return false
	}
}

func (node *DataPathNode) IsAnchorUPF() bool {

	if len(node.DataPathToDN) == 0 {
		return true
	} else {
		return false
	}

}

func (node *DataPathNode) GetUpLink() (link *DataPathDownLink) {

	return node.DataPathToAN
}

func (node *DataPathNode) GetUpLinkPDR() (pdr *PDR) {
	return node.DataPathToAN.UpLinkPDR
}

func (node *DataPathNode) GetUpLinkFAR() (far *FAR) {
	return node.DataPathToAN.UpLinkPDR.FAR
}

func (node *DataPathNode) GetParent() (parent *DataPathNode) {
	return node.DataPathToAN.To
}

func (node *DataPathNode) PathToString() string {
	if node == nil {
		return ""
	}
	return node.UPF.NodeID.ResolveNodeIdToIp().String() + " -> " + node.DownLinkTunnel.SrcEndPoint.PathToString()
}
