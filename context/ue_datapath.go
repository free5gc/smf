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

	logger.PduSessLog.Infoln("In NewUEPreConfigPaths")

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
		var ue_node, child_node, parent_node *DataPathNode
		for idx, node_name := range path.UPF {

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
				child_node.AddPrev(parent_node)
			default:
				child_node.AddPrev(parent_node)
				ue_node = child_node
				child_name := path.UPF[idx+1]
				child_node, err = NewUEDataPathNode(child_name)

				if err != nil {
					logger.CtxLog.Warnln(err)
				}

				ue_node.AddNext(child_node)
			}

			parent_node = ue_node

		}

		logger.CtxLog.Traceln("New data path added")
		logger.CtxLog.Traceln("\n" + dataPath.ToString() + "\n")
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
