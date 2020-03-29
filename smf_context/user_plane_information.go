package smf_context

import (
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/src/smf/factory"
	"gofree5gc/src/smf/logger"
	"net"
	"reflect"

	"github.com/google/uuid"
)

type UserPlaneInformation struct {
	UPNodes              map[string]*UPNode
	UPFs                 map[string]*UPNode
	AccessNetwork        map[string]*UPNode
	UPFIPToName          map[string]string
	UPFsID               map[string]string    // name to id
	UPFsIPtoID           map[string]string    // ip->id table, for speed optimization
	DefaultUserPlanePath map[string][]*UPNode // DNN to Default Path
}

type UPNodeType string

const (
	UPNODE_UPF UPNodeType = "UPF"
	UPNODE_AN  UPNodeType = "AN"
)

// UPNode represent the user plane node
type UPNode struct {
	Type         UPNodeType
	NodeID       pfcpType.NodeID
	UPResourceIP net.IP
	ANIP         net.IP
	Dnn          string
	Links        []*UPNode
	UPF          *UPF
}

func AllocateUPFID() {
	UPFsID := smfContext.UserPlaneInformation.UPFsID
	UPFsIPtoID := smfContext.UserPlaneInformation.UPFsIPtoID

	for upf_name, upf_node := range smfContext.UserPlaneInformation.UPFs {
		upfid := uuid.New().String()
		upfip := upf_node.NodeID.ResolveNodeIdToIp().String()

		UPFsID[upf_name] = upfid
		UPFsIPtoID[upfip] = upfid
	}
}

func processUPTopology(upTopology *factory.UserPlaneInformation) {
	nodePool := make(map[string]*UPNode)
	upfPool := make(map[string]*UPNode)
	anPool := make(map[string]*UPNode)
	upfIpMap := make(map[string]string)

	for name, node := range upTopology.UPNodes {
		upNode := new(UPNode)
		upNode.Type = UPNodeType(node.Type)
		switch upNode.Type {
		case UPNODE_AN:
			upNode.ANIP = net.ParseIP(node.ANIP)
			anPool[name] = upNode
		case UPNODE_UPF:
			//ParseIp() always return 16 bytes
			//so we can't use the length of return ip to seperate IPv4 and IPv6
			//This is just a work around
			var ip net.IP
			if net.ParseIP(node.NodeID).To4() == nil {

				ip = net.ParseIP(node.NodeID)
			} else {

				ip = net.ParseIP(node.NodeID).To4()
			}

			switch len(ip) {
			case net.IPv4len:
				upNode.NodeID = pfcpType.NodeID{
					NodeIdType:  pfcpType.NodeIdTypeIpv4Address,
					NodeIdValue: ip,
				}
			case net.IPv6len:
				upNode.NodeID = pfcpType.NodeID{
					NodeIdType:  pfcpType.NodeIdTypeIpv6Address,
					NodeIdValue: ip,
				}
			default:
				upNode.NodeID = pfcpType.NodeID{
					NodeIdType:  pfcpType.NodeIdTypeFqdn,
					NodeIdValue: []byte(node.NodeID),
				}
			}

			upfPool[name] = upNode
		default:
			logger.InitLog.Warningf("invalid UPNodeType: %s\n", upNode.Type)
		}

		nodePool[name] = upNode

		ipStr := upNode.NodeID.ResolveNodeIdToIp().String()
		upfIpMap[ipStr] = name
	}

	for _, link := range upTopology.Links {
		nodeA := nodePool[link.A]
		nodeB := nodePool[link.B]
		if nodeA == nil || nodeB == nil {
			logger.InitLog.Warningf("UPLink [%s] <=> [%s] not establish\n", link.A, link.B)
			continue
		}
		nodeA.Links = append(nodeA.Links, nodeB)
		nodeB.Links = append(nodeB.Links, nodeA)
	}

	//Initialize each UPF
	for _, upfNode := range upfPool {
		upfNode.UPF = NewUPF(&upfNode.NodeID)
	}

	smfContext.UserPlaneInformation.UPNodes = nodePool
	smfContext.UserPlaneInformation.UPFs = upfPool
	smfContext.UserPlaneInformation.AccessNetwork = anPool
	smfContext.UserPlaneInformation.UPFIPToName = upfIpMap
	smfContext.UserPlaneInformation.UPFsID = make(map[string]string)
	smfContext.UserPlaneInformation.UPFsIPtoID = make(map[string]string)
	smfContext.UserPlaneInformation.DefaultUserPlanePath = make(map[string][]*UPNode)
}

func (upi *UserPlaneInformation) GetUPFNameByIp(ip string) string {

	return upi.UPFIPToName[ip]
}

func (upi *UserPlaneInformation) GetUPFNodeIDByName(name string) pfcpType.NodeID {

	return upi.UPFs[name].NodeID
}

func (upi *UserPlaneInformation) GetUPFNodeByIP(ip string) *UPNode {

	upf_name := upi.GetUPFNameByIp(ip)
	return upi.UPFs[upf_name]
}

func (upi *UserPlaneInformation) GetUPFIDByIP(ip string) string {

	return upi.UPFsIPtoID[ip]
}

func (upi *UserPlaneInformation) GetDefaultUPFTopoByDNN(dnn string) (root *DataPathNode) {

	path, path_exist := upi.DefaultUserPlanePath[dnn]

	if !path_exist {

		return nil
	}

	var lowerBound = 0
	var upperBound = len(path) - 1
	var parent *DataPathNode

	for idx, node := range path {

		dataPathNode := NewDataPathNode()
		dataPathNode.UPF = node.UPF
		dataPathNode.InUse = true
		switch idx {
		case lowerBound:

			root = dataPathNode
			root.DataPathToAN = NewDataPathDownLink()
			parent = dataPathNode
		case upperBound:
			dataPathNode.AddParent(parent)
			dataPathNode.DLDataPathLinkForPSA = NewDataPathUpLink()
			parent.AddChild(dataPathNode)

		default:

			dataPathNode.AddParent(parent)
			parent.AddChild(dataPathNode)
			parent = dataPathNode

		}
	}

	return

}

func (upi *UserPlaneInformation) ExistDefaultPath(dnn string) bool {

	_, exist := upi.DefaultUserPlanePath[dnn]
	return exist
}

func (upi *UserPlaneInformation) GenerateDefaultPath(dnn string) (pathExist bool) {

	var source *UPNode
	var destination *UPNode

	for _, node := range upi.AccessNetwork {

		if node.Type == UPNODE_AN {
			source = node
			break
		}
	}

	if source == nil {
		logger.CtxLog.Errorf("There is no AN Node in config file!")
		return false
	}

	for _, node := range upi.UPFs {

		if node.UPF.UPIPInfo.NetworkInstance != nil {
			node_dnn := string(node.UPF.UPIPInfo.NetworkInstance)
			if node_dnn == dnn {
				destination = node
				break
			}
		}
	}

	if destination == nil {
		logger.CtxLog.Errorf("Can't find UPF with DNN [%s]\n", dnn)
		return false
	}

	//Run DFS
	var visited map[*UPNode]bool
	visited = make(map[*UPNode]bool)

	for _, upNode := range upi.UPNodes {
		visited[upNode] = false
	}

	var path []*UPNode
	path, pathExist = getPathBetween(source, destination, visited)

	if path[0].Type == UPNODE_AN {
		path = path[1:]
	}
	upi.DefaultUserPlanePath[dnn] = path
	return
}

func getPathBetween(cur *UPNode, dest *UPNode, visited map[*UPNode]bool) (path []*UPNode, pathExist bool) {

	visited[cur] = true

	if reflect.DeepEqual(*cur, *dest) {

		path = make([]*UPNode, 0)
		path = append(path, cur)
		pathExist = true
		return
	}

	for _, nodes := range cur.Links {

		if !visited[nodes] {
			path_tail, path_exist := getPathBetween(nodes, dest, visited)

			if path_exist {
				path = make([]*UPNode, 0)
				path = append(path, cur)

				path = append(path, path_tail...)
				pathExist = true

				return
			}
		}
	}

	return nil, false

}
