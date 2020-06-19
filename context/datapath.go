package context

import (
	"fmt"
	"free5gc/src/smf/logger"
)

// GTPTunnel represents the GTP tunnel information
type GTPTunnel struct {
	SrcEndPoint  *DataPathNode
	DestEndPoint *DataPathNode

	TEID uint32
	PDR  *PDR
}

type DataPathNode struct {
	UPF *UPF
	//DataPathToAN *DataPathDownLink
	//DataPathToDN map[string]*DataPathUpLink //uuid to DataPathLink

	UpLinkTunnel   *GTPTunnel
	DownLinkTunnel *GTPTunnel
	//for UE Routing Topology
	//for special case:
	//branching & leafnode

	//InUse                bool
	IsBranchingPoint bool
	//DLDataPathLinkForPSA *DataPathUpLink
	//BPUpLinkPDRs         map[string]*DataPathDownLink // uuid to UpLink

	HaveSession bool
}

type DataPath struct {
	//meta data
	Activated         bool
	Destination       Destination
	HasBranchingPoint bool
	//Data Path Double Link List
	FirstDPNode *DataPathNode
}

type DataPathPool map[int64]*DataPath

type Destination struct {
	DestinationIP   string
	DestinationPort string
	Url             string
}

func NewDataPathNode() (node *DataPathNode) {
	node = &DataPathNode{}
	return
}

func NewDataPath() (dataPath *DataPath) {
	dataPath = &DataPath{
		Destination: Destination{
			DestinationIP:   "",
			DestinationPort: "",
			Url:             "",
		},
	}

	return
}

func NewDataPathPool() (pool DataPathPool) {
	pool = make(map[int64]*DataPath)
	return
}

func (node *DataPathNode) AddNext(next *DataPathNode) {
	node.DownLinkTunnel.SrcEndPoint = next

	return
}
func (node *DataPathNode) AddPrev(prev *DataPathNode) {
	node.UpLinkTunnel.SrcEndPoint = prev

	return
}

func (node *DataPathNode) Next() (next *DataPathNode) {
	next = node.DownLinkTunnel.SrcEndPoint
	return
}

func (node *DataPathNode) Prev() (prev *DataPathNode) {
	prev = node.DownLinkTunnel.SrcEndPoint
	return
}

func (node *DataPathNode) SetUpLinkSrcNode(smContext *SMContext, nextUpLinkNode *DataPathNode) (err error) {

	node.UpLinkTunnel = new(GTPTunnel)
	node.UpLinkTunnel.SrcEndPoint = nextUpLinkNode
	node.UpLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	node.UpLinkTunnel.PDR, err = destUPF.AddPDR()
	smContext.PutPDRtoPFCPSession(destUPF.NodeID, node.UpLinkTunnel.PDR)

	if err != nil {
		logger.CtxLog.Errorln("allocate UpLinkTunnel.MatchedPDR", err)
	}

	teid, _ := destUPF.GenerateTEID()
	node.UpLinkTunnel.TEID = teid
	return
}

func (node *DataPathNode) SetDownLinkSrcNode(smContext *SMContext, nextDownLinkNode *DataPathNode) (err error) {

	node.DownLinkTunnel = new(GTPTunnel)
	node.DownLinkTunnel.SrcEndPoint = nextDownLinkNode
	node.DownLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	node.DownLinkTunnel.PDR, err = destUPF.AddPDR()
	smContext.PutPDRtoPFCPSession(destUPF.NodeID, node.DownLinkTunnel.PDR)

	if err != nil {
		logger.CtxLog.Errorln("allocate DownLinkTunnel.MatchedPDR", err)
	}

	teid, _ := destUPF.GenerateTEID()
	node.DownLinkTunnel.TEID = teid

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

	if node.Prev() == nil {
		return true
	} else {
		return false
	}
}

func (node *DataPathNode) IsAnchorUPF() bool {

	if node.Next() == nil {
		return true
	} else {
		return false
	}

}

func (node *DataPathNode) GetUpLinkPDR() (pdr *PDR) {
	return node.UpLinkTunnel.PDR
}

func (node *DataPathNode) GetUpLinkFAR() (far *FAR) {
	return node.UpLinkTunnel.PDR.FAR
}

func (node *DataPathNode) PathToString() string {
	if node == nil {
		return ""
	}
	return node.UPF.NodeID.ResolveNodeIdToIp().String() + " -> " + node.Next().PathToString()
}
