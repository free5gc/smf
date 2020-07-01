package context

import (
	"fmt"
	"free5gc/lib/idgenerator"
	"free5gc/src/smf/factory"
	"free5gc/src/smf/logger"
)

type UEPreConfigPaths struct {
	DataPathPool    DataPathPool
	PathIDGenerator *idgenerator.IDGenerator
}

func NewUEDataPathNode(name string) (node *DataPathNode, err error) {

	upNodes := smfContext.UserPlaneInformation.UPNodes

	if _, exist := upNodes[name]; !exist {
		err = fmt.Errorf("UPNode %s isn't exist in smfcfg.conf, but in UERouting.yaml!", name)
		return nil, err
	}

	node = &DataPathNode{
		UPF:            upNodes[name].UPF,
		UpLinkTunnel:   &GTPTunnel{},
		DownLinkTunnel: &GTPTunnel{},
	}
	return
}

func NewUEPreConfigPaths(SUPI string, paths []factory.Path) (uePreConfigPaths *UEPreConfigPaths, err error) {

	ueDataPathPool := NewDataPathPool()
	lowerBound := 0
	pathIDGenerator := idgenerator.NewGenerator(1, 2147483647)

	for idx, path := range paths {
		upperBound := len(path.UPF) - 1
		dataPath := NewDataPath()

		if idx == 0 {
			dataPath.IsDefaultPath = true
		}
		pathID, err := pathIDGenerator.Allocate()
		if err != nil {
			logger.CtxLog.Warnf("Allocate pathID error: %+v", err)
			return nil, err
		}

		dataPath.Destination.DestinationIP = path.DestinationIP
		dataPath.Destination.DestinationPort = path.DestinationPort
		ueDataPathPool[pathID] = dataPath

		for idx, node_name := range path.UPF {

			var ue_node, child_node, parent_node *DataPathNode
			var err error

			ue_node, err = NewUEDataPathNode(node_name)

			switch idx {
			case lowerBound:
				child_name := path.UPF[idx+1]
				child_node, err = NewUEDataPathNode(child_name)

				if err != nil {
					logger.CtxLog.Warnln(err)
				}

				ue_node.AddNext(child_node)
				dataPath.FirstDPNode = ue_node

			case upperBound:
				parent_name := path.UPF[idx-1]
				parent_node, err = NewUEDataPathNode(parent_name)

				if err != nil {
					logger.CtxLog.Warnln(err)
				}

				ue_node.AddPrev(parent_node)

			default:
				child_name := path.UPF[idx+1]
				child_node, err = NewUEDataPathNode(child_name)
				if err != nil {
					logger.CtxLog.Warnln(err)
				}

				parent_name := path.UPF[idx-1]
				parent_node, err = NewUEDataPathNode(parent_name)

				if err != nil {
					logger.CtxLog.Warnln(err)
				}

				ue_node.AddNext(child_node)
				ue_node.AddPrev(parent_node)
			}

		}
	}

	uePreConfigPaths = &UEPreConfigPaths{
		DataPathPool:    ueDataPathPool,
		PathIDGenerator: pathIDGenerator,
	}
	return
}

func GetUEPreConfigPaths(SUPI string) *UEPreConfigPaths {
	return smfContext.UEPreConfigPathPool[SUPI]
}

func CheckUEHasPreConfig(SUPI string) (exist bool) {
	_, exist = smfContext.UEPreConfigPathPool[SUPI]
	fmt.Println("CheckUEHasPreConfig")
	fmt.Println(smfContext.UEPreConfigPathPool)
	return
}

// func (uepg *UEDataPathGraph) FindBranchingPoints() {
// 	//BFS algo implementation
// 	const (
// 		WHITE int = 0
// 		GREY  int = 1
// 		BLACK int = 2
// 	)

// 	num_of_nodes := len(uepg.Graph)

// 	color := make(map[string]int)
// 	distance := make(map[string]int)
// 	queue := make(chan *DataPathNode, num_of_nodes)

// 	for _, node := range uepg.Graph {

// 		node_id, _ := node.GetUPFID()
// 		color[node_id] = WHITE
// 		distance[node_id] = num_of_nodes + 1
// 	}

// 	cur_idx := 0 // start point
// 	for j := 0; j < num_of_nodes; j++ {

// 		node_id, _ := uepg.Graph[cur_idx].GetUPFID()
// 		if color[node_id] == WHITE {
// 			color[node_id] = GREY
// 			distance[node_id] = 0

// 			queue <- uepg.Graph[cur_idx]
// 			for len(queue) > 0 {
// 				node := <-queue
// 				branchingCount := 0
// 				for child_id, child_link := range node.DataPathToDN {

// 					if color[child_id] == WHITE {
// 						color[child_id] = GREY
// 						distance[child_id] = distance[node_id] + 1
// 						queue <- child_link.To
// 					}

// 					if color[child_id] == WHITE || color[child_id] == GREY {
// 						branchingCount += 1
// 					}
// 				}

// 				if node.DataPathToAN != nil {

// 					parent := node.DataPathToAN.To
// 					parent_id, _ := node.DataPathToAN.To.GetUPFID()

// 					if color[parent_id] == WHITE {
// 						color[parent_id] = GREY
// 						distance[parent_id] = distance[node_id] + 1
// 						queue <- parent
// 					}

// 					if color[parent_id] == WHITE || color[parent_id] == GREY {
// 						branchingCount += 1
// 					}
// 				}

// 				if branchingCount >= 2 {
// 					node.IsBranchingPoint = true
// 				}
// 				color[node_id] = BLACK
// 			}
// 		}

// 		//Keep finding other connected components
// 		cur_idx = j
// 	}

// }

// func GetUERoutingGraph(SUPI string) *UEDataPathGraph {
// 	return smfContext.UEDataPathPools[SUPI]
// }
