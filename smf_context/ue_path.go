package smf_context

import (
	"fmt"
)

type UEPathGraph struct {
	SUPI string
	//Graph map[string]*UEPathNode
	Graph []*UEPathNode
}

type UEPathNode struct {
	UPFName   string
	Neighbors map[string]*UEPathNode
}

func (node *UEPathNode) AddNeighbor(neighbor *UEPathNode) {
	//check if neighbor exist first

	if _, exist := node.Neighbors[neighbor.UPFName]; !exist {
		node.Neighbors[neighbor.UPFName] = neighbor
	}
}

func NewUEPathNode(name string) (node *UEPathNode) {
	node = &UEPathNode{
		UPFName:   name,
		Neighbors: make(map[string]*UEPathNode),
	}
	return
}

func (uepg *UEPathGraph) PrintGraph() {

	fmt.Println("SUPI: ", uepg.SUPI)
	for _, node := range uepg.Graph {
		fmt.Println("\tUPF: ")
		fmt.Println("\t\t", node.UPFName)
		fmt.Println("\tNeighbors: ")
		for neighbor_name := range node.Neighbors {

			fmt.Println("\t\t", neighbor_name)
		}
	}
}

func NewUEPathGraph(SUPI string) (UEPGraph *UEPathGraph) {

	UEPGraph = new(UEPathGraph)
	UEPGraph.Graph = make([]*UEPathNode, 0)
	UEPGraph.SUPI = SUPI

	paths := smfContext.UERoutingPaths[SUPI]
	lowerBound := 0

	NodeCreated := make(map[string]*UEPathNode)

	for _, path := range paths {
		upperBound := len(path.UPF) - 1
		for idx, node_name := range path.UPF {

			var ue_node *UEPathNode
			var child_node *UEPathNode
			var parent_node *UEPathNode
			var exist bool

			if ue_node, exist = NodeCreated[node_name]; !exist {
				ue_node = NewUEPathNode(node_name)
				NodeCreated[node_name] = ue_node
				UEPGraph.Graph = append(UEPGraph.Graph, ue_node)
			}

			switch idx {
			case lowerBound:
				child_name := path.UPF[idx+1]

				if child_node, exist = NodeCreated[child_name]; !exist {
					child_node = NewUEPathNode(child_name)
					NodeCreated[child_name] = child_node
					UEPGraph.Graph = append(UEPGraph.Graph, child_node)
				}

				//fmt.Printf("%+v\n", ue_node)
				ue_node.AddNeighbor(child_node)

			case upperBound:
				parent_name := path.UPF[idx-1]

				if parent_node, exist = NodeCreated[parent_name]; !exist {
					parent_node = NewUEPathNode(parent_name)
					NodeCreated[parent_name] = parent_node
					UEPGraph.Graph = append(UEPGraph.Graph, parent_node)
				}

				//fmt.Printf("%+v\n", ue_node)
				ue_node.AddNeighbor(parent_node)
			default:
				child_name := path.UPF[idx+1]

				if child_node, exist = NodeCreated[child_name]; !exist {
					child_node = NewUEPathNode(child_name)
					NodeCreated[child_name] = child_node
					UEPGraph.Graph = append(UEPGraph.Graph, child_node)
				}

				parent_name := path.UPF[idx-1]

				if parent_node, exist = NodeCreated[parent_name]; !exist {
					parent_node = NewUEPathNode(parent_name)
					NodeCreated[parent_name] = parent_node
					UEPGraph.Graph = append(UEPGraph.Graph, parent_node)
				}

				//fmt.Printf("%+v\n", ue_node)
				ue_node.AddNeighbor(child_node)
				ue_node.AddNeighbor(parent_node)
			}

		}
	}

	return
}

func (uepg *UEPathGraph) FindBranchingPoints() {
	const (
		WHITE int = 0
		GREY  int = 1
		BLACK int = 2
	)

}
