package context

import (
	"errors"
	"fmt"

	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
)

type UEDefaultPaths struct {
	AnchorUPFs      []*UPF // list of UPF name
	DefaultPathPool DefaultPathPool
}

type DefaultPathPool map[string]*DataPath // key: UPF name

func NewUEDefaultPaths(upi *UserPlaneInformation, topology []factory.UPLink) (*UEDefaultPaths, error) {
	logger.MainLog.Traceln("In NewUEDefaultPaths")

	defaultPathPool := make(map[string]*DataPath)
	source, err := findSourceInTopology(upi, topology)
	if err != nil {
		return nil, err
	}
	destinations, err := extractAnchorUPFForULCL(source, topology)
	if err != nil {
		return nil, err
	}
	for _, destination := range destinations {
		path, errgenerate := generateDefaultDataPath(source, destination.GetName(), topology)
		if errgenerate != nil {
			return nil, errgenerate
		}
		defaultPathPool[destination.GetName()] = path
	}
	defautlPaths := &UEDefaultPaths{
		AnchorUPFs:      destinations,
		DefaultPathPool: defaultPathPool,
	}
	return defautlPaths, nil
}

func findSourceInTopology(upi *UserPlaneInformation, topology []factory.UPLink) (string, error) {
	sourceList := make([]string, 0)
	for key, node := range upi.AccessNetwork {
		if node.GetType() == UPNODE_AN {
			sourceList = append(sourceList, key)
		}
	}
	for _, anName := range sourceList {
		for _, link := range topology {
			if link.A == anName || link.B == anName {
				// if multiple gNBs exist, select one according to some criterion
				logger.InitLog.Debugf("%s is AN", anName)
				return anName, nil
			}
		}
	}
	return "", errors.New("cannot find AN node in topology")
}

func extractAnchorUPFForULCL(source string, topology []factory.UPLink) ([]*UPF, error) {
	upfList := make([]*UPF, 0)
	visited := make(map[string]bool)
	queue := make([]string, 0)

	queue = append(queue, source)
	queued := make(map[string]bool)
	queued[source] = true

	for {
		node := queue[0]
		queue = queue[1:]
		findNewLink := false
		for _, link := range topology {
			if link.A == node {
				if !queued[link.B] {
					queue = append(queue, link.B)
					queued[link.B] = true
					findNewLink = true
				}
				if !visited[link.B] {
					findNewLink = true
				}
			}
			if link.B == node {
				if !queued[link.A] {
					queue = append(queue, link.A)
					queued[link.A] = true
					findNewLink = true
				}
				if !visited[link.A] {
					findNewLink = true
				}
			}
		}
		visited[node] = true
		if !findNewLink {
			logger.InitLog.Debugf("%s is Anchor UPF", node)
			upf := GetUserPlaneInformation().NameToUPNode[node]
			upfList = append(upfList, upf.(*UPF))
		}
		if len(queue) == 0 {
			break
		}
	}
	if len(upfList) == 0 {
		return nil, fmt.Errorf("[extractAnchorUPFForULCL] no ULCL PSA candidates")
	}

	upfList = GetUserPlaneInformation().sortUPFListByName(upfList)

	if len(upfList) == 0 {
		return nil, fmt.Errorf("[extractAnchorUPFForULCL] ULCL PSA candidates are empty after sorting")
	}
	return upfList, nil
}

func generateDefaultDataPath(source string, destination string, topology []factory.UPLink) (*DataPath, error) {
	allPaths, _ := getAllPathByNodeName(source, destination, topology)
	if len(allPaths) == 0 {
		return nil, fmt.Errorf("path does not exist: %s to %s", source, destination)
	}

	dataPath := NewDataPath()
	lowerBound := 0
	var parentNode *DataPathNode = nil

	// if multiple Paths exist, select one according to some criterion
	for idx, nodeName := range allPaths[0] {
		newUeNode, err := NewUEDataPathNode(nodeName)
		if err != nil {
			return nil, err
		}
		if idx == lowerBound {
			dataPath.FirstDPNode = newUeNode
		}
		if parentNode != nil {
			newUeNode.AddPrev(parentNode)
			parentNode.AddNext(newUeNode)
		}
		parentNode = newUeNode
	}
	logger.CtxLog.Tracef("New default data path (%s to %s): ", source, destination)
	logger.CtxLog.Traceln("\n" + dataPath.String() + "\n")
	return dataPath, nil
}

func getAllPathByNodeName(src, dest string, links []factory.UPLink) (map[int][]string, int) {
	visited := make(map[string]bool)
	allPaths := make(map[int][]string)
	count := 0
	var findPath func(src, dest string, links []factory.UPLink, currentPath []string)

	findPath = func(src, dest string, links []factory.UPLink, currentPath []string) {
		if visited[src] {
			return
		}
		visited[src] = true
		currentPath = append(currentPath, src)
		logger.InitLog.Traceln("current path:", currentPath)
		if src == dest {
			cpy := make([]string, len(currentPath))
			copy(cpy, currentPath)
			allPaths[count] = cpy[1:]
			count++
			logger.InitLog.Traceln("all path:", allPaths)
			visited[src] = false
			return
		}
		for _, link := range links {
			// search A to B only
			if link.A == src {
				findPath(link.B, dest, links, currentPath)
			}
		}
		visited[src] = false
	}

	findPath(src, dest, links, []string{})
	return allPaths, count
}

func (dfp *UEDefaultPaths) GetDefaultPath(upfName string) *DataPath {
	firstNode := dfp.DefaultPathPool[upfName].CopyFirstDPNode()
	dataPath := &DataPath{
		Activated:     false,
		IsDefaultPath: true,
		Destination:   dfp.DefaultPathPool[upfName].Destination,
		FirstDPNode:   firstNode,
	}
	return dataPath
}
