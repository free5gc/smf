package smf_context

import "fmt"

type DataPathNode struct {
	UPF  *UPF
	Prev *DataPathLink
	Next map[string]*DataPathLink //uuid to DataPathLink

	//for UE Routing Topology
	//for special case:
	//branching & leafnode
	IsBranchingPoint     bool
	DLDataPathLinkForPSA *DataPathLink
}

type DataPathLink struct {
	To *DataPathNode

	// Filter Rules
	DestinationIP   string
	DestinationPort string

	// related context
	PDR *PDR
}

func NewDataPathNode() (node *DataPathNode) {

	node = &DataPathNode{
		UPF:                  nil,
		Next:                 make(map[string]*DataPathLink),
		Prev:                 nil,
		IsBranchingPoint:     false,
		DLDataPathLinkForPSA: nil,
	}
	return
}

func NewDataPathLink() (link *DataPathLink) {

	link = &DataPathLink{
		To:              nil,
		DestinationIP:   "",
		DestinationPort: "",
		PDR:             nil,
	}
	return
}

func (node *DataPathNode) AddChild(child *DataPathNode) (err error) {

	child_id, err := child.GetUPFID()

	if err != nil {
		return err
	}

	if _, exist := node.Next[child_id]; !exist {

		child_link := &DataPathLink{
			To:              child,
			DestinationIP:   "",
			DestinationPort: "",
			PDR:             nil,
		}
		node.Next[child_id] = child_link
	}

	return
}

func (node *DataPathNode) AddParent(parent *DataPathNode) (err error) {

	parent_ip := parent.GetNodeIP()
	var exist bool

	if _, exist = smfContext.UserPlaneInformation.UPFsIPtoID[parent_ip]; !exist {
		err = fmt.Errorf("UPNode IP %s doesn't exist in smfcfg.conf, please sync the config files!", parent_ip)
		return err
	}

	if node.Prev == nil {

		parent_link := &DataPathLink{
			To:              parent,
			DestinationIP:   "",
			DestinationPort: "",
			PDR:             nil,
		}

		node.Prev = parent_link
	}

	return
}

func (node *DataPathNode) AddDestinationOfChild(child *DataPathNode, Dest *DataPathLink) (err error) {

	child_id, err := child.GetUPFID()

	if err != nil {
		return err
	}
	if child_link, exist := node.Next[child_id]; exist {

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

func (node *DataPathNode) PrintPath() {

	upi := smfContext.UserPlaneInformation
	fmt.Println(upi.GetUPFNameByIp(node.GetNodeIP()))

	for _, node := range node.Next {

		node.To.PrintPath()
	}
}

func (node *DataPathNode) IsANUPF() bool {

	if node.Prev.To == nil {
		return true
	} else {
		return false
	}
}

func (node *DataPathNode) IsAnchorUPF() bool {

	if len(node.Next) == 0 {
		return true
	} else {
		return false
	}

}

func (node *DataPathNode) GetUpLinkPDR() (pdr *PDR) {

	return node.Prev.PDR
}

func (node *DataPathNode) GetUpLinkFAR() (far *FAR) {

	return node.Prev.PDR.FAR
}

func (node *DataPathNode) GetParent() (parent *DataPathNode) {

	return node.Prev.To
}
