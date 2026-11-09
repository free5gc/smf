package context

import (
	"fmt"
	"net"

	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

type PFCPSessionResponseStatus int

const (
	SessionEstablishSuccess PFCPSessionResponseStatus = iota
	SessionEstablishFailed
	SessionUpdateSuccess
	SessionUpdateFailed
	SessionReleaseSuccess
	SessionReleaseFailed
)

type FSEID struct {
	IP   net.IP
	SEID uint64
}

type PFCPSessionContext struct {
	PDRs       map[uint16]*PDR
	NodeID     pfcptype.NodeID
	LocalSEID  uint64
	RemoteSEID uint64
	// The F-TEIDs that a UPF which allocates them chose, by the CHOOSE ID of
	// the PDRs that asked for them (TS 29.244 5.5.3).
	chosenFTEIDs map[uint8]*pfcptype.FTEID
}

// UplinkChooseID is the CHOOSE ID by which the PDRs of the session's tunnel at
// a UPF that allocates the F-TEIDs ask for one F-TEID (TS 29.244 5.5.3).
const UplinkChooseID uint8 = 1

// ForgetRemoteSession clears what belonged to the PFCP session at the UPF: its
// SEID and the F-TEIDs that the UPF chose.
func (pfcpSessionContext *PFCPSessionContext) ForgetRemoteSession() {
	pfcpSessionContext.RemoteSEID = 0
	pfcpSessionContext.chosenFTEIDs = nil
}

// SetChosenFTEID records the F-TEID that the UPF chose for the PDRs that asked
// with chooseID.
func (pfcpSessionContext *PFCPSessionContext) SetChosenFTEID(chooseID uint8, fteid *pfcptype.FTEID) {
	if pfcpSessionContext.chosenFTEIDs == nil {
		pfcpSessionContext.chosenFTEIDs = make(map[uint8]*pfcptype.FTEID)
	}
	pfcpSessionContext.chosenFTEIDs[chooseID] = fteid
}

// NameChosenFTEID gives a PDR to create that asks by a CHOOSE ID the F-TEID
// that the UPF already chose for that CHOOSE ID: one F-TEID for the tunnel.
// One that asks for an F-TEID of its own, with no CHOOSE ID, keeps asking.
func (pfcpSessionContext *PFCPSessionContext) NameChosenFTEID(pdr *PDR) {
	fteid := pdr.PDI.LocalFTeid
	if fteid == nil || !fteid.Ch {
		return
	}
	if chosen := pfcpSessionContext.chosenFTEIDs[fteid.ChooseId]; chosen != nil {
		copied := *chosen
		pdr.PDI.LocalFTeid = &copied
	}
}

// newLocalFTEID is the Local F-TEID of a PDR of the session's tunnel at upf:
// the SMF's own, ip and teid, or, at a UPF that allocates the F-TEIDs (FTUP),
// a request to choose it by chooseID (TS 29.244 5.5.1).
func newLocalFTEID(upf *UPF, chooseID uint8, ip net.IP, teid uint32) *pfcptype.FTEID {
	if upf.AllocatesFTEID() {
		return &pfcptype.FTEID{Ch: true, Chid: true, V4: true, ChooseId: chooseID}
	}
	return &pfcptype.FTEID{V4: true, Ipv4Address: ip, Teid: teid}
}

// ChosenFTEID is the F-TEID that upf chose for the session's PDRs that asked
// with chooseID, or nil.
func (smContext *SMContext) ChosenFTEID(upf *UPF, chooseID uint8) *pfcptype.FTEID {
	if sessionContext := smContext.PFCPContext[upf.NodeID.ResolveNodeIdToIp().String()]; sessionContext != nil {
		return sessionContext.chosenFTEIDs[chooseID]
	}
	return nil
}

func (pfcpSessionContext *PFCPSessionContext) String() string {
	str := "\n"
	for pdrID, pdr := range pfcpSessionContext.PDRs {
		str += fmt.Sprintln("PDR ID: ", pdrID)
		str += fmt.Sprintf("PDR: %v\n", pdr)
	}

	str += fmt.Sprintln("Node ID: ", pfcpSessionContext.NodeID.ResolveNodeIdToIp().String())
	str += fmt.Sprintln("LocalSEID: ", pfcpSessionContext.LocalSEID)
	str += fmt.Sprintln("RemoteSEID: ", pfcpSessionContext.RemoteSEID)
	str += "\n"

	return str
}

func (pfcpSessionResponseStatus PFCPSessionResponseStatus) String() string {
	switch pfcpSessionResponseStatus {
	case SessionUpdateSuccess:
		return "SessionUpdateSuccess"
	case SessionUpdateFailed:
		return "SessionUpdateFailed"
	case SessionReleaseSuccess:
		return "SessionReleaseSuccess"
	case SessionReleaseFailed:
		return "SessionReleaseFailed"
	default:
		return "Unknown PFCP Session Response Status"
	}
}

func (smContext *SMContext) GetNodeIDByLocalSEID(seid uint64) pfcptype.NodeID {
	for _, pfcpCtx := range smContext.PFCPContext {
		if pfcpCtx.LocalSEID == seid {
			return pfcpCtx.NodeID
		}
	}

	return pfcptype.NodeID{}
}

func (smContext *SMContext) AllocateLocalSEIDForUPPath(path UPPath) {
	for _, upNode := range path {
		NodeIDtoIP := upNode.NodeID.ResolveNodeIdToIp().String()
		if _, exist := smContext.PFCPContext[NodeIDtoIP]; !exist {
			allocatedSEID := AllocateLocalSEID()

			smContext.PFCPContext[NodeIDtoIP] = &PFCPSessionContext{
				PDRs:      make(map[uint16]*PDR),
				NodeID:    upNode.NodeID,
				LocalSEID: allocatedSEID,
			}

			seidSMContextMap.Store(allocatedSEID, smContext)
		}
	}
}

func (smContext *SMContext) AllocateLocalSEIDForDataPath(dataPath *DataPath) {
	logger.PduSessLog.Traceln("In AllocateLocalSEIDForDataPath")
	for node := dataPath.FirstDPNode; node != nil; node = node.Next() {
		NodeIDtoIP := node.UPF.NodeID.ResolveNodeIdToIp().String()
		logger.PduSessLog.Traceln("NodeIDtoIP: ", NodeIDtoIP)
		if _, exist := smContext.PFCPContext[NodeIDtoIP]; !exist {
			allocatedSEID := AllocateLocalSEID()
			smContext.PFCPContext[NodeIDtoIP] = &PFCPSessionContext{
				PDRs:      make(map[uint16]*PDR),
				NodeID:    node.UPF.NodeID,
				LocalSEID: allocatedSEID,
			}

			seidSMContextMap.Store(allocatedSEID, smContext)
		}
	}
}

func (smContext *SMContext) PutPDRtoPFCPSession(nodeID pfcptype.NodeID, pdr *PDR) error {
	NodeIDtoIP := nodeID.ResolveNodeIdToIp().String()
	if pfcpSessCtx, exist := smContext.PFCPContext[NodeIDtoIP]; exist {
		smContext.Log.Tracef("PutPDRtoPFCPSession [%+v]", pdr)
		pfcpSessCtx.PDRs[pdr.PDRID] = pdr
	} else {
		return fmt.Errorf("can't find PFCPContext[%s] to put PDR(%d)", NodeIDtoIP, pdr.PDRID)
	}
	return nil
}

func (smContext *SMContext) RemovePDRfromPFCPSession(nodeID pfcptype.NodeID, pdr *PDR) {
	NodeIDtoIP := nodeID.ResolveNodeIdToIp().String()
	pfcpSessCtx := smContext.PFCPContext[NodeIDtoIP]
	delete(pfcpSessCtx.PDRs, pdr.PDRID)
}
