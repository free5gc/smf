package smf_context

import "fmt"

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

func (node *DataPathNode) AddChild(child *DataPathNode) (err error) {

	child_id, err := child.GetUPFID()

	if err != nil {
		return err
	}

	if _, exist := node.Next[child_id]; !exist {

		child_link := &DataPathLink{
			To: child,
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

	if node.Prev != nil {

		parent_link := &DataPathLink{
			To: parent,
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
