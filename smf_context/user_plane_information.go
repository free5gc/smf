package smf_context

import (
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/src/smf/factory"
	"gofree5gc/src/smf/logger"
	"net"

	"github.com/google/uuid"
)

type UserPlaneInformation struct {
	UPNodes       map[string]*UPNode
	UPFs          map[string]*UPNode
	AccessNetwork map[string]*UPNode
	UPFIPToName   map[string]string
	UPFsID        map[string]uint32 // name to id
	UPFsIPtoID    map[string]uint32 // ip->id table, for speed optimization
}

type UPNodeType string

const (
	UPNODE_UPF UPNodeType = "UPF"
	UPNODE_AN  UPNodeType = "AN"
)

// UPNode represent the user plane node
type UPNode struct {
	Type           UPNodeType
	NodeID         pfcpType.NodeID
	UPResourceIP   net.IP
	ANIP           net.IP
	Dnn            string
	Links          []*UPNode
	UPFInformation *UPFInformation
}

func AllocateUPFID() {
	UPFsID := smfContext.UserPlaneInformation.UPFsID
	UPFsIPtoID := smfContext.UserPlaneInformation.UPFsIPtoID

	for up_name, up_node := range smfContext.UserPlaneInformation.UPFs {
		upfid := uuid.New().ID()
		upfip := up_node.NodeID.ResolveNodeIdToIp().String()

		UPFsID[up_name] = upfid
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
			ip := net.ParseIP(node.NodeID)
			switch len(ip) {
			case net.IPv4len:
				upNode.NodeID = pfcpType.NodeID{
					NodeIdType:  pfcpType.NodeIdTypeIpv4Address,
					NodeIdValue: ip,
				}
			case net.IPv6len:
				upNode.NodeID = pfcpType.NodeID{
					NodeIdType:  pfcpType.NodeIdTypeIpv4Address,
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
		nodeB.Links = append(nodeA.Links, nodeB)
	}
	smfContext.UserPlaneInformation.UPNodes = nodePool
	smfContext.UserPlaneInformation.UPFs = upfPool
	smfContext.UserPlaneInformation.AccessNetwork = anPool
	smfContext.UserPlaneInformation.UPFIPToName = upfIpMap
	smfContext.UserPlaneInformation.UPFsID = make(map[string]uint32)
	smfContext.UserPlaneInformation.UPFsIPtoID = make(map[string]uint32)
}

func (upi *UserPlaneInformation) GetUPFIPByName(name string) []byte {

	return upi.UPFs[name].NodeID.NodeIdValue
}

func (upi *UserPlaneInformation) GetUPFNodeIDByName(name string) pfcpType.NodeID {

	return upi.UPFs[name].NodeID
}
