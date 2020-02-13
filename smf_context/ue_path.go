package smf_context

import (
	"fmt"
)

type UEPathGraph struct {
	SUPI  string
	Graph []*UEPathNode
}

type UEPathNode struct {
	UPFName          string
	Neighbors        map[string]*UEPathNode
	IsBranchingPoint bool
}

func (node *UEPathNode) AddNeighbor(neighbor *UEPathNode) {
	//check if neighbor exist first

	if _, exist := node.Neighbors[neighbor.UPFName]; !exist {
		node.Neighbors[neighbor.UPFName] = neighbor
	}
}

func NewUEPathNode(name string) (node *UEPathNode) {
	node = &UEPathNode{
		UPFName:          name,
		Neighbors:        make(map[string]*UEPathNode),
		IsBranchingPoint: false,
	}
	return
}

func (uepg *UEPathGraph) PrintGraph() {

	fmt.Println("SUPI: ", uepg.SUPI)
	for _, node := range uepg.Graph {
		fmt.Println("\tUPF: ")
		fmt.Println("\t\t", node.UPFName)
		fmt.Println("\tBranching Point: ")
		fmt.Println("\t\t", node.IsBranchingPoint)
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
	//BFS algo implementation
	const (
		WHITE int = 0
		GREY  int = 1
		BLACK int = 2
	)

	num_of_nodes := len(uepg.Graph)

	color := make(map[string]int)
	distance := make(map[string]int)
	queue := make(chan *UEPathNode, num_of_nodes)

	for _, node := range uepg.Graph {

		color[node.UPFName] = WHITE
		distance[node.UPFName] = num_of_nodes + 1
	}

	cur_idx := 0 // start point
	for j := 0; j < num_of_nodes; j++ {

		cur_name := uepg.Graph[cur_idx].UPFName
		if color[cur_name] == WHITE {
			color[cur_name] = GREY
			distance[cur_name] = 0

			queue <- uepg.Graph[cur_idx]
			for len(queue) > 0 {
				node := <-queue
				branchingCount := 0
				for neighbor_name, neighbor_node := range node.Neighbors {

					if color[neighbor_name] == WHITE {
						color[neighbor_name] = GREY
						distance[neighbor_name] = distance[cur_name] + 1
						queue <- neighbor_node
					}

					if color[neighbor_name] == WHITE || color[neighbor_name] == GREY {
						branchingCount += 1
					}
				}

				if branchingCount >= 2 {
					node.IsBranchingPoint = true
				}
				color[node.UPFName] = BLACK
			}
		}

		//Keep finding other connected components
		cur_idx = j
	}

}
