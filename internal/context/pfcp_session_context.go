package context

import (
	"fmt"
	"net"
	"sync"

	"github.com/free5gc/pfcp/pfcpUdp"
)

type PFCPState struct {
	UPF     *UPF
	PDRList []*PDR
	FARList []*FAR
	BARList []*BAR
	QERList []*QER
	URRList []*URR
}

type SendPfcpResult struct {
	Status PFCPSessionResponseStatus
	RcvMsg *pfcpUdp.Message
	Err    error
	Source string
}

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

// stores PDRs and SEIDs for the PFCP connection of one PDU session
// mainly used for session establishment and restoration after UPF reboot
type PFCPSessionContext struct {
	// stores the PDRs that are modified during session management
	PDRs         map[uint16]*PDR // map pdrId to PDR
	UPF          *UPF
	UeIP         net.IP
	PDUSessionID int32
	LocalSEID    uint64
	RemoteSEID   uint64
	Restoring    sync.Mutex
}

func (pfcpSessionContext *PFCPSessionContext) String() string {
	str := "\n"
	str += fmt.Sprintf("PFCPSessionContext for UPF[%s]\n", pfcpSessionContext.UPF.GetNodeIDString())
	for _, pdr := range pfcpSessionContext.PDRs {
		str += pdr.String()
	}
	str += fmt.Sprintln("LocalSEID: ", pfcpSessionContext.LocalSEID)
	str += fmt.Sprintln("RemoteSEID: ", pfcpSessionContext.RemoteSEID)
	str += "\n"

	return str
}

func (pfcpSessionContext *PFCPSessionContext) PDUSessionParams() string {
	return fmt.Sprintf("PDU Session[ UEIP %s | ID %d ]", pfcpSessionContext.UeIP.String(), pfcpSessionContext.PDUSessionID)
}

func (pfcpSessionContext *PFCPSessionContext) MarkAsSyncedToUPFRecursive() {
	for _, pdr := range pfcpSessionContext.PDRs {
		pdr.SetStateRecursive(RULE_SYNCED)
	}
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
