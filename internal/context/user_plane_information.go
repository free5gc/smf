package context

import (
	"errors"
	"fmt"
	"math/rand"
	"net"
	"reflect"
	"sort"
	"sync"

	"github.com/google/uuid"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
)

// UserPlaneInformation store userplane topology
type UserPlaneInformation struct {
	Mu                        sync.RWMutex // protect UPF and topology structure
	UPNodes                   map[string]UPNodeInterface
	UPFs                      map[string]*UPF
	AccessNetwork             map[string]*GNB
	UPFIPToName               map[string]string
	UPFsID                    map[string]uuid.UUID         // name to id
	UPFsIPtoID                map[string]uuid.UUID         // ip->id table, for speed optimization
	DefaultUserPlanePath      map[string]UPPath            // DNN to Default Path
	DefaultUserPlanePathToUPF map[string]map[string]UPPath // DNN and UPF to Default Path
}

type UPNodeType string

const (
	UPNODE_UPF UPNodeType = "UPF"
	UPNODE_AN  UPNodeType = "AN"
)

// UPNode represents a gNB or UPF in the user plane node topology
// UPF and gNB structs embed this ("interitance")
type UPNode struct {
	Name   string
	Type   UPNodeType
	ID     uuid.UUID
	NodeID pfcpType.NodeID
	Dnn    string
	Links  UPPath
}

// UPF and gNB structs implement this interface to provide common methods
// i.e., methods all UPNodes should have
type UPNodeInterface interface {
	String() string
	GetName() string
	GetID() uuid.UUID
	GetType() UPNodeType
	GetDnn() string
	GetLinks() UPPath
	AddLink(link UPNodeInterface) bool
	RemoveLink(link UPNodeInterface) bool
	RemoveLinkByIndex(index int) bool
	GetNodeID() pfcpType.NodeID
	GetNodeIDString() string
}

// UPPath represents the User Plane Node Sequence of this path
type UPPath []UPNodeInterface

func (upPath UPPath) String() string {
	str := ""
	for i, upNode := range upPath {
		str += fmt.Sprintf("Node %d: %s", i, upNode)
	}
	return str
}

func (upPath UPPath) NodeInPath(upNode UPNodeInterface) int {
	for i, u := range upPath {
		if u == upNode {
			return i
		}
	}
	return -1
}

// static/ global function to convert a NodeID to a string depending on the NodeID type
func NodeIDToString(nodeID pfcpType.NodeID) string {
	switch nodeID.NodeIdType {
	case pfcpType.NodeIdTypeIpv4Address, pfcpType.NodeIdTypeIpv6Address:
		return nodeID.IP.String()
	case pfcpType.NodeIdTypeFqdn:
		return nodeID.FQDN
	default:
		logger.CtxLog.Errorf("nodeID has unknown type %d", nodeID.NodeIdType)
		return ""
	}
}

func AllocateUPFID() {
	UPFsID := smfContext.UserPlaneInformation.UPFsID
	UPFsIPtoID := smfContext.UserPlaneInformation.UPFsIPtoID

	for upfName, upfNode := range smfContext.UserPlaneInformation.UPFs {
		upfid := upfNode.GetID()
		upfip := upfNode.GetNodeIDString()

		UPFsID[upfName] = upfid
		UPFsIPtoID[upfip] = upfid
	}
}

// the config has a single string for NodeID,
// check its nature and create either IPv4, IPv6, or FQDN NodeID type
func ConfigToNodeID(configNodeID string) (pfcpType.NodeID, error) {
	logger.CfgLog.Tracef("Converting config input %s to NodeID", configNodeID)

	ip := net.ParseIP(configNodeID)
	var err error

	if ip == nil {
		// might be in CIDR notation
		ip, _, err = net.ParseCIDR(configNodeID)
	}

	if err == nil && ip != nil {
		// valid IP address, check the type
		if ip.To4() != nil {
			logger.CfgLog.Tracef("%s is IPv4", configNodeID)
			return pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeIpv4Address,
				IP:         ip.To4(),
			}, nil
		} else {
			logger.CfgLog.Tracef("%s is IPv6", configNodeID)
			return pfcpType.NodeID{
				NodeIdType: pfcpType.NodeIdTypeIpv6Address,
				IP:         ip,
			}, nil
		}
	}

	// might be an FQDN, try to resolve it
	ips, err := net.LookupIP(configNodeID)
	if err != nil {
		return pfcpType.NodeID{}, fmt.Errorf("input %s is not a valid IP address or resolvable FQDN", configNodeID)
	}
	if len(ips) == 0 {
		return pfcpType.NodeID{}, fmt.Errorf("no IP addresses found for the given FQDN %s", configNodeID)
	}

	logger.CfgLog.Tracef("%s is FQDN", configNodeID)

	return pfcpType.NodeID{
		NodeIdType: pfcpType.NodeIdTypeFqdn,
		FQDN:       configNodeID,
	}, nil
}

// NewUserPlaneInformation process the configuration then returns a new instance of UserPlaneInformation
func NewUserPlaneInformation(upTopology *factory.UserPlaneInformation) *UserPlaneInformation {
	nodePool := make(map[string]UPNodeInterface)
	upfPool := make(map[string]*UPF)
	anPool := make(map[string]*GNB)
	upfIPMap := make(map[string]string)
	allUEIPPools := []*UeIPPool{}

	for name, node := range upTopology.UPNodes {
		nodeID, err := ConfigToNodeID(node.NodeID)
		if err != nil {
			logger.InitLog.Fatalf("[NewUserPlaneInformation] cannot parse %s NodeID from config: %+v", name, err)
		}
		upNode := &UPNode{
			Name:   name,
			Type:   UPNodeType(node.Type),
			ID:     uuid.New(),
			NodeID: nodeID,
			Dnn:    node.Dnn,
		}
		switch upNode.Type {
		case UPNODE_AN:
			gNB := &GNB{
				UPNode: *upNode,
				ANIP:   upNode.NodeID.ResolveNodeIdToIp(),
			}
			anPool[name] = gNB
			nodePool[name] = gNB
		case UPNODE_UPF:
			upf := NewUPF(upNode, node.InterfaceUpfInfoList)

			snssaiInfos := make([]*SnssaiUPFInfo, 0)
			for _, snssaiInfoConfig := range node.SNssaiInfos {
				snssaiInfo := SnssaiUPFInfo{
					SNssai: &SNssai{
						Sst: snssaiInfoConfig.SNssai.Sst,
						Sd:  snssaiInfoConfig.SNssai.Sd,
					},
					DnnList: make([]*DnnUPFInfoItem, 0),
				}

				for _, dnnInfoConfig := range snssaiInfoConfig.DnnUpfInfoList {
					ueIPPools := make([]*UeIPPool, 0)
					staticUeIPPools := make([]*UeIPPool, 0)
					for _, pool := range dnnInfoConfig.Pools {
						ueIPPool := NewUEIPPool(pool)
						if ueIPPool == nil {
							logger.InitLog.Fatalf("invalid pools value: %+v", pool)
						} else {
							ueIPPools = append(ueIPPools, ueIPPool)
							allUEIPPools = append(allUEIPPools, ueIPPool)
						}
					}
					for _, staticPool := range dnnInfoConfig.StaticPools {
						staticUeIPPool := NewUEIPPool(staticPool)
						if staticUeIPPool == nil {
							logger.InitLog.Fatalf("invalid pools value: %+v", staticPool)
						} else {
							staticUeIPPools = append(staticUeIPPools, staticUeIPPool)
							for _, dynamicUePool := range ueIPPools {
								if dynamicUePool.ueSubNet.Contains(staticUeIPPool.ueSubNet.IP) {
									if err = dynamicUePool.Exclude(staticUeIPPool); err != nil {
										logger.InitLog.Fatalf("exclude static Pool[%s] failed: %v",
											staticUeIPPool.ueSubNet, err)
									}
								}
							}
						}
					}
					for _, pool := range ueIPPools {
						if pool.pool.Min() != pool.pool.Max() {
							if err = pool.pool.Reserve(pool.pool.Min(), pool.pool.Min()); err != nil {
								logger.InitLog.Errorf("Remove network address failed for %s: %s", pool.ueSubNet.String(), err)
							}
							if err = pool.pool.Reserve(pool.pool.Max(), pool.pool.Max()); err != nil {
								logger.InitLog.Errorf("Remove network address failed for %s: %s", pool.ueSubNet.String(), err)
							}
						}
						logger.InitLog.Debugf("%d-%s %s %s",
							snssaiInfo.SNssai.Sst, snssaiInfo.SNssai.Sd,
							dnnInfoConfig.Dnn, pool.dump())
					}
					snssaiInfo.DnnList = append(snssaiInfo.DnnList, &DnnUPFInfoItem{
						Dnn:             dnnInfoConfig.Dnn,
						DnaiList:        dnnInfoConfig.DnaiList,
						PduSessionTypes: dnnInfoConfig.PduSessionTypes,
						UeIPPools:       ueIPPools,
						StaticIPPools:   staticUeIPPools,
					})
				}
				snssaiInfos = append(snssaiInfos, &snssaiInfo)
			}
			upf.SNssaiInfos = snssaiInfos
			upfPool[name] = upf
			nodePool[name] = upf
			upfIPMap[upf.GetNodeIDString()] = name
		default:
			logger.InitLog.Warningf("invalid UPNodeType: %s\n", upNode.Type)
		}
	}

	if isOverlap(allUEIPPools) {
		logger.InitLog.Fatalf("overlap cidr value between UPFs")
	}

	userplaneInformation := &UserPlaneInformation{
		UPNodes:                   nodePool,
		UPFs:                      upfPool,
		AccessNetwork:             anPool,
		UPFIPToName:               upfIPMap,
		UPFsID:                    make(map[string]uuid.UUID),
		UPFsIPtoID:                make(map[string]uuid.UUID),
		DefaultUserPlanePath:      make(map[string]UPPath),
		DefaultUserPlanePathToUPF: make(map[string]map[string]UPPath),
	}

	userplaneInformation.LinksFromConfiguration(upTopology)

	return userplaneInformation
}

func (upi *UserPlaneInformation) UpNodesToConfiguration() map[string]*factory.UPNode {
	nodes := make(map[string]*factory.UPNode)
	for name, upNode := range upi.UPNodes {
		node := &factory.UPNode{
			NodeID: upNode.GetNodeIDString(),
			Dnn:    upNode.GetDnn(),
		}
		nodes[name] = node

		switch upNode.GetType() {
		case UPNODE_AN:
			node.Type = "AN"
		case UPNODE_UPF:
			node.Type = "UPF"
			upf := upNode.(*UPF)
			if upf.SNssaiInfos != nil {
				FsNssaiInfoList := make([]*factory.SnssaiUpfInfoItem, 0)
				for _, sNssaiInfo := range upf.SNssaiInfos {
					FDnnUpfInfoList := make([]*factory.DnnUpfInfoItem, 0)
					for _, dnnInfo := range sNssaiInfo.DnnList {
						FUEIPPools := make([]*factory.UEIPPool, 0)
						FStaticUEIPPools := make([]*factory.UEIPPool, 0)
						for _, pool := range dnnInfo.UeIPPools {
							FUEIPPools = append(FUEIPPools, &factory.UEIPPool{
								Cidr: pool.ueSubNet.String(),
							})
						} // for pool
						for _, pool := range dnnInfo.StaticIPPools {
							FStaticUEIPPools = append(FStaticUEIPPools, &factory.UEIPPool{
								Cidr: pool.ueSubNet.String(),
							})
						} // for static pool
						FDnnUpfInfoList = append(FDnnUpfInfoList, &factory.DnnUpfInfoItem{
							Dnn:         dnnInfo.Dnn,
							Pools:       FUEIPPools,
							StaticPools: FStaticUEIPPools,
						})
					} // for dnnInfo
					Fsnssai := &factory.SnssaiUpfInfoItem{
						SNssai: &models.Snssai{
							Sst: sNssaiInfo.SNssai.Sst,
							Sd:  sNssaiInfo.SNssai.Sd,
						},
						DnnUpfInfoList: FDnnUpfInfoList,
					}
					FsNssaiInfoList = append(FsNssaiInfoList, Fsnssai)
				} // for sNssaiInfo
				node.SNssaiInfos = FsNssaiInfoList
			} // if UPF.SNssaiInfos
			FNxList := make([]*factory.InterfaceUpfInfoItem, 0)
			for _, iface := range upf.N3Interfaces {
				endpoints := make([]string, 0)
				// upf.go L90
				if iface.EndpointFQDN != "" {
					endpoints = append(endpoints, iface.EndpointFQDN)
				}
				for _, eIP := range iface.IPv4EndPointAddresses {
					endpoints = append(endpoints, eIP.String())
				}
				FNxList = append(FNxList, &factory.InterfaceUpfInfoItem{
					InterfaceType:    models.UpInterfaceType_N3,
					Endpoints:        endpoints,
					NetworkInstances: iface.NetworkInstances,
				})
			} // for N3Interfaces

			for _, iface := range upf.N9Interfaces {
				endpoints := make([]string, 0)
				// upf.go L90
				if iface.EndpointFQDN != "" {
					endpoints = append(endpoints, iface.EndpointFQDN)
				}
				for _, eIP := range iface.IPv4EndPointAddresses {
					endpoints = append(endpoints, eIP.String())
				}
				FNxList = append(FNxList, &factory.InterfaceUpfInfoItem{
					InterfaceType:    models.UpInterfaceType_N9,
					Endpoints:        endpoints,
					NetworkInstances: iface.NetworkInstances,
				})
			} // N9Interfaces
			node.InterfaceUpfInfoList = FNxList
		default:
			logger.InitLog.Warningf("invalid UPNodeType: %s\n", upNode.GetType())
			node.Type = "Unknown"
		}
	}

	return nodes
}

func (upi *UserPlaneInformation) LinksToConfiguration() []*factory.UPLink {
	links := make([]*factory.UPLink, 0)
	source, err := upi.selectUPPathSource()
	if err != nil {
		logger.InitLog.Errorf("AN Node not found\n")
	} else {
		visited := make(map[UPNodeInterface]bool)
		queue := make(UPPath, 0)
		queue = append(queue, source)
		for {
			node := queue[0]
			queue = queue[1:]
			visited[node] = true
			for _, link := range node.GetLinks() {
				if !visited[link] {
					queue = append(queue, link)
					nodeIpStr := node.GetNodeIDString()
					ipStr := link.GetNodeIDString()
					linkA := upi.UPFIPToName[nodeIpStr]
					linkB := upi.UPFIPToName[ipStr]
					links = append(links, &factory.UPLink{
						A: linkA,
						B: linkB,
					})
				}
			}
			if len(queue) == 0 {
				break
			}
		}
	}
	return links
}

func (upi *UserPlaneInformation) UpNodesFromConfiguration(upTopology *factory.UserPlaneInformation) {
	allUEIPPools := []*UeIPPool{}

	for name, node := range upTopology.UPNodes {
		if _, ok := upi.UPNodes[name]; ok {
			logger.InitLog.Warningf("Node [%s] already exists in SMF.\n", name)
			continue
		}
		nodeID, err := ConfigToNodeID(node.NodeID)
		if err != nil {
			logger.InitLog.Fatalf("[UpNodesFromConfiguration] cannot parse NodeID from config: %+v", err)
		}
		upNode := &UPNode{
			Name:   name,
			Type:   UPNodeType(node.Type),
			ID:     uuid.New(),
			NodeID: nodeID,
			Dnn:    node.Dnn,
		}
		switch upNode.Type {
		case UPNODE_AN:
			gNB := &GNB{
				UPNode: *upNode,
				ANIP:   upNode.NodeID.ResolveNodeIdToIp(),
			}

			upi.AccessNetwork[name] = gNB
			upi.UPNodes[name] = gNB

		case UPNODE_UPF:
			upf := NewUPF(upNode, node.InterfaceUpfInfoList)
			snssaiInfos := make([]*SnssaiUPFInfo, 0)
			for _, snssaiInfoConfig := range node.SNssaiInfos {
				snssaiInfo := &SnssaiUPFInfo{
					SNssai: &SNssai{
						Sst: snssaiInfoConfig.SNssai.Sst,
						Sd:  snssaiInfoConfig.SNssai.Sd,
					},
					DnnList: make([]*DnnUPFInfoItem, 0),
				}

				for _, dnnInfoConfig := range snssaiInfoConfig.DnnUpfInfoList {
					ueIPPools := make([]*UeIPPool, 0)
					staticUeIPPools := make([]*UeIPPool, 0)
					for _, pool := range dnnInfoConfig.Pools {
						ueIPPool := NewUEIPPool(pool)
						if ueIPPool == nil {
							logger.InitLog.Fatalf("invalid pools value: %+v", pool)
						} else {
							ueIPPools = append(ueIPPools, ueIPPool)
						}
					}
					for _, pool := range dnnInfoConfig.StaticPools {
						ueIPPool := NewUEIPPool(pool)
						if ueIPPool == nil {
							logger.InitLog.Fatalf("invalid pools value: %+v", pool)
						} else {
							staticUeIPPools = append(staticUeIPPools, ueIPPool)
							for _, dynamicUePool := range ueIPPools {
								if dynamicUePool.ueSubNet.Contains(ueIPPool.ueSubNet.IP) {
									if err = dynamicUePool.Exclude(ueIPPool); err != nil {
										logger.InitLog.Fatalf("exclude static Pool[%s] failed: %v",
											ueIPPool.ueSubNet, err)
									}
								}
							}
						}
					}
					snssaiInfo.DnnList = append(snssaiInfo.DnnList, &DnnUPFInfoItem{
						Dnn:             dnnInfoConfig.Dnn,
						DnaiList:        dnnInfoConfig.DnaiList,
						PduSessionTypes: dnnInfoConfig.PduSessionTypes,
						UeIPPools:       ueIPPools,
						StaticIPPools:   staticUeIPPools,
					})
				}
				snssaiInfos = append(snssaiInfos, snssaiInfo)
			}
			upf.SNssaiInfos = snssaiInfos
			upi.UPFs[name] = upf

			// AllocateUPFID
			upfid := upf.GetID()
			upfip := upf.GetNodeIDString()
			upi.UPFsID[name] = upfid
			upi.UPFsIPtoID[upfip] = upfid

			upi.UPNodes[name] = upf
			upi.UPFIPToName[upfip] = name

			// collect IP pool of this UPF for later overlap check
			for _, sNssaiInfo := range upf.SNssaiInfos {
				for _, dnnUPFInfo := range sNssaiInfo.DnnList {
					allUEIPPools = append(allUEIPPools, dnnUPFInfo.UeIPPools...)
				}
			}

		default:
			logger.InitLog.Warningf("invalid UPNodeType: %s\n", upNode.Type)
		}
	}

	if isOverlap(allUEIPPools) {
		logger.InitLog.Fatalf("overlap cidr value between UPFs")
	}
}

func (upi *UserPlaneInformation) LinksFromConfiguration(upTopology *factory.UserPlaneInformation) {
	for _, link := range upTopology.Links {
		nodeA := upi.UPNodes[link.A]
		nodeB := upi.UPNodes[link.B]
		if nodeA == nil || nodeB == nil {
			logger.CfgLog.Warningf("One of link edges does not exist. UPLink [%s] <=> [%s] not established\n", link.A, link.B)
			continue
		}
		if nodeInLink(nodeB, nodeA.GetLinks()) != -1 || nodeInLink(nodeA, nodeB.GetLinks()) != -1 {
			logger.InitLog.Warningf("One of link edges already exist. UPLink [%s] <=> [%s] not establish\n", link.A, link.B)
			continue
		}
		nodeA.AddLink(nodeB)
		nodeB.AddLink(nodeA)
	}
}

func (upi *UserPlaneInformation) UpNodeDelete(upNodeName string) {
	upNode, ok := upi.UPNodes[upNodeName]
	if ok {
		logger.InitLog.Infof("UPNode [%s] found. Deleting it.\n", upNodeName)
		if upNode.GetType() == UPNODE_UPF {
			logger.InitLog.Tracef("Delete UPF [%s] from its NodeID.\n", upNodeName)
			RemoveUPFNodeByNodeID(upNode.GetNodeID())
			if _, ok = upi.UPFs[upNodeName]; ok {
				logger.InitLog.Tracef("Delete UPF [%s] from upi.UPFs.\n", upNodeName)
				delete(upi.UPFs, upNodeName)
			}
			for selectionStr, destMap := range upi.DefaultUserPlanePathToUPF {
				for destIp, path := range destMap {
					if nodeInPath(upNode, path) != -1 {
						logger.InitLog.Infof("Invalidate cache entry: DefaultUserPlanePathToUPF[%s][%s].\n", selectionStr, destIp)
						delete(upi.DefaultUserPlanePathToUPF[selectionStr], destIp)
					}
				}
			}
		}
		if upNode.GetType() == UPNODE_AN {
			logger.InitLog.Tracef("Delete AN [%s] from upi.AccessNetwork.\n", upNodeName)
			delete(upi.AccessNetwork, upNodeName)
		}
		logger.InitLog.Tracef("Delete UPNode [%s] from upi.UPNodes.\n", upNodeName)
		delete(upi.UPNodes, upNodeName)

		// update links
		for name, n := range upi.UPNodes {
			if index := nodeInLink(upNode, n.GetLinks()); index != -1 {
				logger.InitLog.Infof("Delete UPLink [%s] <=> [%s].\n", name, upNodeName)
				n.RemoveLinkByIndex(index)
			}
		}
	}
}

func nodeInPath(upNode UPNodeInterface, path UPPath) int {
	for i, u := range path {
		if u == upNode {
			return i
		}
	}
	return -1
}

func nodeInLink(upNode UPNodeInterface, links UPPath) int {
	for i, n := range links {
		if n == upNode {
			return i
		}
	}
	return -1
}

func (upi *UserPlaneInformation) GetUPFNameByIp(ip string) string {
	return upi.UPFIPToName[ip]
}

func (upi *UserPlaneInformation) GetUPFNodeIDByName(name string) pfcpType.NodeID {
	return upi.UPFs[name].NodeID
}

func (upi *UserPlaneInformation) GetUPFNodeByIP(ip string) *UPF {
	upfName := upi.GetUPFNameByIp(ip)
	return upi.UPFs[upfName]
}

func (upi *UserPlaneInformation) GetUPFIDByIP(ip string) uuid.UUID {
	return upi.UPFsIPtoID[ip]
}

func (upi *UserPlaneInformation) GetDefaultUserPlanePathByDNN(selection *UPFSelectionParams) (path UPPath) {
	path, pathExist := upi.DefaultUserPlanePath[selection.String()]
	logger.CtxLog.Tracef("[GetDefaultUserPlanePathByDNN] for %s", selection)
	if pathExist {
		logger.CtxLog.Traceln("[GetDefaultUserPlanePathByDNN] path exists")
		return
	} else {
		logger.CtxLog.Traceln("[GetDefaultUserPlanePathByDNN] path does not exist, generate default path")
		pathExist = upi.GenerateDefaultPath(selection)
		if pathExist {
			return upi.DefaultUserPlanePath[selection.String()]
		}
	}
	return nil
}

func (upi *UserPlaneInformation) GetDefaultUserPlanePathByDNNAndUPF(
	selection *UPFSelectionParams,
	upf *UPF,
) UPPath {
	nodeID := upf.GetNodeIDString()

	if upi.DefaultUserPlanePathToUPF[selection.String()] != nil {
		path, pathExist := upi.DefaultUserPlanePathToUPF[selection.String()][nodeID]
		logger.CtxLog.Tracef("[GetDefaultUserPlanePathByDNNAndUPF] for %s , pathExist: %t", selection.String(), pathExist)
		if pathExist {
			return path
		}
	}
	if pathExist := upi.GenerateDefaultPathToUPF(selection, upf); pathExist {
		return upi.DefaultUserPlanePathToUPF[selection.String()][nodeID]
	}
	return nil
}

func (upi *UserPlaneInformation) ExistDefaultPath(dnn string) bool {
	_, exist := upi.DefaultUserPlanePath[dnn]
	return exist
}

func GenerateDataPath(upPath UPPath) *DataPath {
	if len(upPath) < 1 {
		logger.CtxLog.Errorf("Invalid data path")
		return nil
	}
	lowerBound := 0
	upperBound := len(upPath) - 1
	var root *DataPathNode
	var node *DataPathNode
	var prevDataPathNode *DataPathNode

	for idx, upNode := range upPath {
		node = NewDataPathNode()
		if upNode.GetType() == UPNODE_UPF {
			node.UPF = upNode.(*UPF)
		}

		if idx == lowerBound {
			root = node
			root.AddPrev(nil)
		}
		if idx == upperBound {
			node.AddNext(nil)
		}
		if prevDataPathNode != nil {
			prevDataPathNode.AddNext(node)
			node.AddPrev(prevDataPathNode)
		}
		prevDataPathNode = node
	}

	dataPath := NewDataPath()
	dataPath.FirstDPNode = root
	return dataPath
}

func (upi *UserPlaneInformation) GenerateDefaultPath(selection *UPFSelectionParams) bool {
	var source UPNodeInterface
	var upfCandidates []*UPF

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

	upfCandidates = upi.selectMatchUPF(selection)

	if len(upfCandidates) == 0 {
		logger.CtxLog.Errorf("Can't find UPFs that match %s", selection)
		return false
	} else {
		logger.CtxLog.Tracef("Found %d UPFs that match %s", len(upfCandidates), selection)
	}

	// Run DFS
	visited := make(map[UPNodeInterface]bool)

	if len(upi.UPNodes) < 2 {
		logger.CtxLog.Errorln("No UPNodes in UserPlaneInformation !?")
		return false
	}

	for _, upNode := range upi.UPNodes {
		logger.CtxLog.Tracef("DFS with UPNode %s", upNode)
		visited[upNode] = false
	}

	path, pathExist := getPathBetween(source, upfCandidates[0], visited, selection)
	logger.CtxLog.Tracef("After [getPathBetween]: path exists %t, path %s", pathExist, path)

	if pathExist {
		if path[0].GetType() == UPNODE_AN {
			path = path[1:]
		}
		upi.DefaultUserPlanePath[selection.String()] = path
	}

	return pathExist
}

func (upi *UserPlaneInformation) GenerateDefaultPathToUPF(selection *UPFSelectionParams, destination *UPF) bool {
	var source UPNodeInterface

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

	// Run DFS
	visited := make(map[UPNodeInterface]bool)

	for _, upNode := range upi.UPNodes {
		visited[upNode] = false
	}

	path, pathExist := getPathBetween(source, destination, visited, selection)

	if pathExist {
		if path[0].GetType() == UPNODE_AN {
			path = path[1:]
		}
		if upi.DefaultUserPlanePathToUPF[selection.String()] == nil {
			upi.DefaultUserPlanePathToUPF[selection.String()] = make(map[string]UPPath)
		}
		upi.DefaultUserPlanePathToUPF[selection.String()][destination.GetNodeIDString()] = path
	}

	return pathExist
}

func (upi *UserPlaneInformation) selectMatchUPF(selection *UPFSelectionParams) []*UPF {
	upfList := make([]*UPF, 0)

	for _, upf := range upi.UPFs {
		for _, snssaiInfo := range upf.SNssaiInfos {
			currentSnssai := snssaiInfo.SNssai
			targetSnssai := selection.SNssai

			if currentSnssai.Equal(targetSnssai) {
				for _, dnnInfo := range snssaiInfo.DnnList {
					if dnnInfo.Dnn != selection.Dnn {
						continue
					}
					if selection.Dnai != "" && !dnnInfo.ContainsDNAI(selection.Dnai) {
						continue
					}
					upfList = append(upfList, upf)
					break
				}
			}
		}
	}
	return upfList
}

func getPathBetween(
	cur UPNodeInterface,
	dest UPNodeInterface,
	visited map[UPNodeInterface]bool,
	selection *UPFSelectionParams,
) (path UPPath, pathExist bool) {
	logger.CtxLog.Tracef("[getPathBetween] node %s[%s] and %s[%s]",
		cur.GetName(), cur.GetNodeIDString(), dest.GetName(), dest.GetNodeIDString())
	visited[cur] = true

	if reflect.DeepEqual(cur, dest) {
		path = make(UPPath, 0, 1)
		path = append(path, cur)
		pathExist = true
		logger.CtxLog.Traceln("[getPathBetween] source and destination are equal")
		return path, pathExist
	}

	for _, node := range cur.GetLinks() {
		if !visited[node] {
			if node.GetType() == UPNODE_UPF && !node.(*UPF).isSupportSnssai(selection.SNssai) {
				visited[node] = true
				continue
			}

			path_tail, pathExistBuf := getPathBetween(node, dest, visited, selection)
			pathExist = pathExistBuf
			if pathExist {
				path = make(UPPath, 0, 1+len(path_tail))
				path = append(path, cur)
				path = append(path, path_tail...)

				return path, pathExist
			}
		}
	}

	return nil, false
}

// this function select PSA by SNSSAI, DNN and DNAI exlude IP
func (upi *UserPlaneInformation) selectAnchorUPF(source UPNodeInterface, selection *UPFSelectionParams) []*UPF {
	// UPFSelectionParams may have static IP, but we would not match static IP in "MatchedSelection" function
	upfList := make([]*UPF, 0)
	visited := make(map[UPNodeInterface]bool)
	queue := make(UPPath, 0)
	selectionForIUPF := &UPFSelectionParams{
		Dnn:    selection.Dnn,
		SNssai: selection.SNssai,
	}

	queue = append(queue, source)
	for {
		node := queue[0]
		queue = queue[1:]
		findNewNode := false
		visited[node] = true
		for _, link := range node.GetLinks() {
			if link.GetType() == UPNODE_UPF && !visited[link] {
				if link.(*UPF).MatchedSelection(selectionForIUPF) {
					queue = append(queue, link)
					findNewNode = true
					break
				}
			}
		}
		if !findNewNode {
			// if new node is AN type not need to add upList
			if node.GetType() == UPNODE_UPF && node.(*UPF).MatchedSelection(selection) {
				upfList = append(upfList, node.(*UPF))
			}
		}

		if len(queue) == 0 {
			break
		}
	}
	return upfList
}

func (upi *UserPlaneInformation) sortUPFListByName(upfList []*UPF) []*UPF {
	keys := make([]string, 0, len(upi.UPFs))
	for k := range upi.UPFs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sortedUpList := make([]*UPF, 0)
	for _, name := range keys {
		for _, node := range upfList {
			if name == upi.GetUPFNameByIp(node.GetNodeIDString()) {
				sortedUpList = append(sortedUpList, node)
			}
		}
	}
	return sortedUpList
}

func (upi *UserPlaneInformation) selectUPPathSource() (UPNodeInterface, error) {
	// if multiple gNBs exist, select one according to some criterion
	for _, node := range upi.AccessNetwork {
		if node.Type == UPNODE_AN {
			return node, nil
		}
	}
	return nil, errors.New("AN Node not found")
}

// SelectUPFAndAllocUEIP will return anchor UPF, allocated UE IP and use/not use static IP
func (upi *UserPlaneInformation) SelectUPFAndAllocUEIP(selection *UPFSelectionParams) (*UPF, net.IP, bool) {
	source, err := upi.selectUPPathSource()
	if err != nil {
		return nil, nil, false
	}
	upfList := upi.selectAnchorUPF(source, selection)
	listLength := len(upfList)
	if listLength == 0 {
		logger.CtxLog.Warnf("Can't find UPF with DNN[%s] S-NSSAI[sst: %d sd: %s] DNAI[%s]\n", selection.Dnn,
			selection.SNssai.Sst, selection.SNssai.Sd, selection.Dnai)
		return nil, nil, false
	}
	upfList = upi.sortUPFListByName(upfList)
	sortedUPFList := createUPFListForSelection(upfList)
	for _, upf := range sortedUPFList {
		logger.CtxLog.Debugf("check start UPF: %s",
			upi.GetUPFNameByIp(upf.GetNodeIDString()))
		if upf.UPFStatus != AssociatedSetUpSuccess {
			logger.CtxLog.Infof("PFCP Association not yet Established with: %s",
				upi.GetUPFNameByIp(upf.GetNodeIDString()))
			continue
		}
		pools, useStaticIPPool := getUEIPPool(upf, selection)
		if len(pools) == 0 {
			continue
		}
		sortedPoolList := createPoolListForSelection(pools)
		for _, pool := range sortedPoolList {
			logger.CtxLog.Debugf("check start UEIPPool(%+v)", pool.ueSubNet)
			addr := pool.Allocate(selection.PDUAddress)
			if addr != nil {
				logger.CtxLog.Infof("Selected UPF: %s",
					upi.GetUPFNameByIp(upf.GetNodeIDString()))
				return upf, addr, useStaticIPPool
			}
			// if all addresses in pool are used, search next pool
			logger.CtxLog.Debug("check next pool")
		}
		// if all addresses in UPF are used, search next UPF
		logger.CtxLog.Debug("check next upf")
	}
	// checked all UPFs
	logger.CtxLog.Warnf("UE IP pool exhausted for DNN[%s] S-NSSAI[sst: %d sd: %s] DNAI[%s]\n", selection.Dnn,
		selection.SNssai.Sst, selection.SNssai.Sd, selection.Dnai)
	return nil, nil, false
}

func createUPFListForSelection(inputList []*UPF) (outputList []*UPF) {
	offset := rand.Intn(len(inputList))
	return append(inputList[offset:], inputList[:offset]...)
}

func createPoolListForSelection(inputList []*UeIPPool) (outputList []*UeIPPool) {
	offset := rand.Intn(len(inputList))
	return append(inputList[offset:], inputList[:offset]...)
}

// getUEIPPool will return IP pools and use/not use static IP pool
func getUEIPPool(upf *UPF, selection *UPFSelectionParams) ([]*UeIPPool, bool) {
	for _, snssaiInfo := range upf.SNssaiInfos {
		currentSnssai := snssaiInfo.SNssai
		targetSnssai := selection.SNssai

		if currentSnssai.Equal(targetSnssai) {
			for _, dnnInfo := range snssaiInfo.DnnList {
				if dnnInfo.Dnn == selection.Dnn {
					if selection.Dnai != "" && !dnnInfo.ContainsDNAI(selection.Dnai) {
						continue
					}
					if selection.PDUAddress != nil {
						// return static ue ip pool
						for _, ueIPPool := range dnnInfo.StaticIPPools {
							if ueIPPool.ueSubNet.Contains(selection.PDUAddress) {
								// return match IPPools
								return []*UeIPPool{ueIPPool}, true
							}
						}

						// return dynamic ue ip pool
						for _, ueIPPool := range dnnInfo.UeIPPools {
							if ueIPPool.ueSubNet.Contains(selection.PDUAddress) {
								logger.CfgLog.Infof("cannot find selected IP in static pool[%v], use dynamic pool[%+v]",
									dnnInfo.StaticIPPools, dnnInfo.UeIPPools)
								return []*UeIPPool{ueIPPool}, false
							}
						}

						return nil, false
					}

					// if no specify static PDU Address
					return dnnInfo.UeIPPools, false
				}
			}
		}
	}
	return nil, false
}

func (upi *UserPlaneInformation) ReleaseUEIP(upf *UPF, addr net.IP, static bool) {
	pool := findPoolByAddr(upf, addr, static)
	if pool == nil {
		// nothing to do
		logger.CtxLog.Warnf("Fail to release UE IP address: %v to UPF: %s",
			upi.GetUPFNameByIp(upf.GetNodeIDString()), addr)
		return
	}
	pool.Release(addr)
}

func findPoolByAddr(upf *UPF, addr net.IP, static bool) *UeIPPool {
	for _, snssaiInfo := range upf.SNssaiInfos {
		for _, dnnInfo := range snssaiInfo.DnnList {
			if static {
				for _, pool := range dnnInfo.StaticIPPools {
					if pool.ueSubNet.Contains(addr) {
						return pool
					}
				}
			} else {
				for _, pool := range dnnInfo.UeIPPools {
					if pool.ueSubNet.Contains(addr) {
						return pool
					}
				}
			}
		}
	}
	return nil
}
