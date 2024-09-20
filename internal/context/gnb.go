package context

import (
	"fmt"
	"net"

	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/logger"
	"github.com/google/uuid"
)

// embeds the UPNode struct ("inheritance")
// implements UPNodeInterface
type GNB struct {
	UPNode
	ANIP net.IP
}

func (gNB *GNB) GetName() string {
	return gNB.Name
}

func (gNB *GNB) GetID() uuid.UUID {
	return gNB.ID
}

func (gNB *GNB) GetType() UPNodeType {
	return gNB.Type
}

func (gNB *GNB) GetDnn() string {
	return gNB.Dnn
}

func (gNB *GNB) String() string {
	str := "gNB {\n"
	prefix := "  "
	str += prefix + fmt.Sprintf("Name: %s\n", gNB.Name)
	str += prefix + fmt.Sprintf("ANIP: %s\n", gNB.ANIP)
	str += prefix + fmt.Sprintf("ID: %s\n", gNB.ID)
	str += prefix + fmt.Sprintf("NodeID: %s\n", gNB.GetNodeIDString())
	str += prefix + fmt.Sprintf("Dnn: %s\n", gNB.Dnn)
	str += prefix + fmt.Sprintln("Links:")
	for _, link := range gNB.Links {
		str += prefix + fmt.Sprintf("-- %s: %s\n", link.GetName(), link.GetNodeIDString())
	}
	str += "}"
	return str
}

func (gNB *GNB) GetNodeIDString() string {
	switch gNB.NodeID.NodeIdType {
	case pfcpType.NodeIdTypeIpv4Address, pfcpType.NodeIdTypeIpv6Address:
		return gNB.NodeID.IP.String()
	case pfcpType.NodeIdTypeFqdn:
		return gNB.NodeID.FQDN
	default:
		logger.CtxLog.Errorf("nodeID has unknown type %d", gNB.NodeID.NodeIdType)
		return ""
	}
}

func (gNB *GNB) GetNodeID() pfcpType.NodeID {
	return gNB.NodeID
}

func (gNB *GNB) GetLinks() UPPath {
	return gNB.Links
}

func (gNB *GNB) AddLink(link UPNodeInterface) bool {
	for _, existingLink := range gNB.Links {
		if link.GetName() == existingLink.GetName() {
			logger.CfgLog.Warningf("UPLink [%s] <=> [%s] already exists, skip\n", existingLink.GetName(), link.GetName())
			return false
		}
	}
	gNB.Links = append(gNB.Links, link)
	return true
}

func (gNB *GNB) RemoveLink(link UPNodeInterface) bool {
	for i, existingLink := range gNB.Links {
		if link.GetName() == existingLink.GetName() && existingLink.GetNodeIDString() == link.GetNodeIDString() {
			logger.CfgLog.Warningf("Remove UPLink [%s] <=> [%s]\n", existingLink.GetName(), link.GetName())
			gNB.Links = append(gNB.Links[:i], gNB.Links[i+1:]...)
			return true
		}
	}
	return false
}

func (gNB *GNB) RemoveLinkByIndex(index int) bool {
	gNB.Links[index] = gNB.Links[len(gNB.Links)-1]
	return true
}
