package smf_context

import (
	"fmt"
)

type UEDataPathGraph struct {
	SUPI  string
	Graph []*DataPathNode
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

	parent_ip := parent.UPF.NodeID.ResolveNodeIdToIp().String()
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
	node_ip := node.UPF.NodeID.ResolveNodeIdToIp().String()
	var exist bool

	if id, exist = smfContext.UserPlaneInformation.UPFsIPtoID[node_ip]; !exist {
		err = fmt.Errorf("UPNode IP %s doesn't exist in smfcfg.conf, please sync the config files!", node_ip)
		return "", err
	}

	return id, nil

}

// func (node *UEPathNode) AddEndPointOfChild(neighbor *UEPathNode, EndPoint *UEPathEndPoint) {

// 	if _, exist := node.EndPointOfEachChild[neighbor.UPFName]; !exist {
// 		node.EndPointOfEachChild[neighbor.UPFName] = EndPoint
// 	}
// }

// func (node *UEPathNode) RmbParent(parent string) {

// 	node.Parent = parent
// }

// //Add End Point Info to of child node to the map "EndPointOfEachChild"
// //If the node is leaf node, it will add the end point info for itself name.
// func (node *UEPathNode) AddEndPointOfChild(neighbor *UEPathNode, EndPoint *UEPathEndPoint) {

// 	if _, exist := node.EndPointOfEachChild[neighbor.UPFName]; !exist {
// 		node.EndPointOfEachChild[neighbor.UPFName] = EndPoint
// 	}
// }

// func (node *UEPathNode) GetChild() []*UEPathNode {

// 	child := make([]*UEPathNode, 0)
// 	for upfName, upfNode := range node.Neighbors {
// 		if upfName != node.Parent {
// 			child = append(child, upfNode)
// 		}
// 	}

// 	return child
// }

// func (node *UEPathNode) IsLeafNode() bool {

// 	if len(node.Neighbors) == 1 {

// 		if _, exist := node.Neighbors[node.Parent]; exist {
// 			return true
// 		}
// 	}

// 	return false
// }

func NewUEDataPathNode(name string) (node *DataPathNode, err error) {

	upNodes := smfContext.UserPlaneInformation.UPNodes

	if _, exist := upNodes[name]; !exist {
		err = fmt.Errorf("UPNode %s isn't exist in smfcfg.conf, but in UERouting.yaml!", name)
		return nil, err
	}

	fmt.Println("In NewUEDataPathNode: ")
	fmt.Println("New node name: ", name)
	fmt.Println(upNodes)
	fmt.Println("New node IP: ", upNodes[name].NodeID.ResolveNodeIdToIp().String())

	node = &DataPathNode{
		UPF:              upNodes[name].UPF,
		Next:             make(map[string]*DataPathLink),
		Prev:             nil,
		IsBranchingPoint: false,
	}
	return
}

//check a given upf name is a branching point or not
// func (uepg *UEPathGraph) IsBranchingPoint(name string) bool {

// 	for _, upfNode := range uepg.Graph {

// 		if name == upfNode.UPFName {
// 			return upfNode.IsBranchingPoint
// 		}
// 	}

// 	return false
// }

func (uepg *UEDataPathGraph) PrintGraph() {

	fmt.Println("SUPI: ", uepg.SUPI)
	for _, node := range uepg.Graph {
		fmt.Println("\tUPF IP: ")
		fmt.Println("\t\t", node.UPF.NodeID.ResolveNodeIdToIp().String())

		// fmt.Println("\tBranching Point: ")
		// fmt.Println("\t\t", node.IsBranchingPoint)

		if node.Prev != nil {
			fmt.Println("\tParent IP: ")
			fmt.Println("\t\t", node.Prev.To.UPF.NodeID.ResolveNodeIdToIp().String())
		}

		if node.Next != nil {
			fmt.Println("\tChildren IP: ")
			for _, child_link := range node.Next {

				fmt.Println("\t\t", child_link.To.UPF.NodeID.ResolveNodeIdToIp().String())
				fmt.Println("\t\tDestination IP: ", child_link.DestinationIP)
				fmt.Println("\t\tDestination Port: ", child_link.DestinationPort)
			}
		}
	}
}

func NewUEDataPathGraph(SUPI string) (UEPGraph *UEDataPathGraph, err error) {

	UEPGraph = new(UEDataPathGraph)
	UEPGraph.Graph = make([]*DataPathNode, 0)
	UEPGraph.SUPI = SUPI

	paths := smfContext.UERoutingPaths[SUPI]
	lowerBound := 0

	NodeCreated := make(map[string]*DataPathNode)

	for _, path := range paths {
		upperBound := len(path.UPF) - 1

		DataEndPoint := &DataPathLink{
			DestinationIP:   path.DestinationIP,
			DestinationPort: path.DestinationPort,
		}
		for idx, node_name := range path.UPF {

			var ue_node, child_node, parent_node *DataPathNode
			var exist bool
			var err error

			if ue_node, exist = NodeCreated[node_name]; !exist {

				ue_node, err = NewUEDataPathNode(node_name)

				if err != nil {
					return nil, err
				}
				NodeCreated[node_name] = ue_node
				UEPGraph.Graph = append(UEPGraph.Graph, ue_node)
			}

			switch idx {
			case lowerBound:
				child_name := path.UPF[idx+1]

				if child_node, exist = NodeCreated[child_name]; !exist {
					child_node, err = NewUEDataPathNode(child_name)

					if err != nil {
						return nil, err
					}
					NodeCreated[child_name] = child_node
					UEPGraph.Graph = append(UEPGraph.Graph, child_node)
				}

				//fmt.Printf("%+v\n", ue_node)
				ue_node.AddChild(child_node)
				ue_node.AddDestinationOfChild(child_node, DataEndPoint)

			case upperBound:
				parent_name := path.UPF[idx-1]

				if parent_node, exist = NodeCreated[parent_name]; !exist {
					parent_node, err = NewUEDataPathNode(parent_name)

					if err != nil {
						return nil, err
					}
					NodeCreated[parent_name] = parent_node
					UEPGraph.Graph = append(UEPGraph.Graph, parent_node)
				}

				//fmt.Printf("%+v\n", ue_node)
				ue_node.AddParent(parent_node)
			default:
				child_name := path.UPF[idx+1]

				if child_node, exist = NodeCreated[child_name]; !exist {
					child_node, err = NewUEDataPathNode(child_name)

					if err != nil {
						return nil, err
					}
					NodeCreated[child_name] = child_node
					UEPGraph.Graph = append(UEPGraph.Graph, child_node)
				}

				parent_name := path.UPF[idx-1]

				if parent_node, exist = NodeCreated[parent_name]; !exist {
					parent_node, err = NewUEDataPathNode(parent_name)

					if err != nil {
						return nil, err
					}
					NodeCreated[parent_name] = parent_node
					UEPGraph.Graph = append(UEPGraph.Graph, parent_node)
				}

				//fmt.Printf("%+v\n", ue_node)
				ue_node.AddChild(child_node)
				ue_node.AddDestinationOfChild(child_node, DataEndPoint)
				ue_node.AddParent(parent_node)
			}

		}
	}

	return
}

// func (uepg *UEPathGraph) FindBranchingPoints() {
// 	//BFS algo implementation
// 	const (
// 		WHITE int = 0
// 		GREY  int = 1
// 		BLACK int = 2
// 	)

// 	num_of_nodes := len(uepg.Graph)

// 	color := make(map[string]int)
// 	distance := make(map[string]int)
// 	queue := make(chan *UEPathNode, num_of_nodes)

// 	for _, node := range uepg.Graph {

// 		color[node.UPFName] = WHITE
// 		distance[node.UPFName] = num_of_nodes + 1
// 	}

// 	cur_idx := 0 // start point
// 	for j := 0; j < num_of_nodes; j++ {

// 		cur_name := uepg.Graph[cur_idx].UPFName
// 		if color[cur_name] == WHITE {
// 			color[cur_name] = GREY
// 			distance[cur_name] = 0

// 			queue <- uepg.Graph[cur_idx]
// 			for len(queue) > 0 {
// 				node := <-queue
// 				branchingCount := 0
// 				for neighbor_name, neighbor_node := range node.Neighbors {

// 					if color[neighbor_name] == WHITE {
// 						color[neighbor_name] = GREY
// 						distance[neighbor_name] = distance[cur_name] + 1
// 						queue <- neighbor_node
// 					}

// 					if color[neighbor_name] == WHITE || color[neighbor_name] == GREY {
// 						branchingCount += 1
// 					}
// 				}

// 				if branchingCount >= 2 {
// 					node.IsBranchingPoint = true
// 				}
// 				color[node.UPFName] = BLACK
// 			}
// 		}

// 		//Keep finding other connected components
// 		cur_idx = j
// 	}

// }
