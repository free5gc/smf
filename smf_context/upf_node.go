package smf_context

import (
	"fmt"
	"gofree5gc/lib/pfcp/pfcpType"
	"net"
	"reflect"
)

var upfPool map[string]*UPFInformation

func init() {
	upfPool = make(map[string]*UPFInformation)
}

type UPTunnel struct {
	Node  *UPFInformation
	ULPDR *PDR
	DLPDR *PDR

	ULTEID uint32
	DLTEID uint32
}

type UPFInformation struct {
	NodeID   pfcpType.NodeID
	UPIPInfo pfcpType.UserPlaneIPResourceInformation

	pdrPool         map[uint16]*PDR
	farPool         map[uint32]*FAR
	barPool         map[uint8]*BAR
	urrPool         map[uint32]*URR
	qerPool         map[uint32]*QER
	pdrCount        uint16
	farCount        uint32
	barCount        uint8
	urrCount        uint32
	qerCount        uint32
	TEIDCount       uint32
	pdrIdReuseQueue []uint16
	farIdReuseQueue []uint32
	barIdReuseQueue []uint8
}

func AddUPF(nodeId *pfcpType.NodeID) (upf *UPFInformation) {
	upf = new(UPFInformation)
	key, err := generateUpfIdFromNodeId(nodeId)

	if err != nil {
		fmt.Println("[SMF] Error occurs while calling AddUPF")
		return
	}

	upfPool[key] = upf
	fmt.Println("[SMF] Add UPF!")
	upf.NodeID = *nodeId
	upf.pdrPool = make(map[uint16]*PDR)
	upf.farPool = make(map[uint32]*FAR)
	upf.barPool = make(map[uint8]*BAR)
	upf.qerPool = make(map[uint32]*QER)
	upf.urrPool = make(map[uint32]*URR)
	upf.pdrIdReuseQueue = make([]uint16, 0)
	upf.farIdReuseQueue = make([]uint32, 0)
	upf.barIdReuseQueue = make([]uint8, 0)

	return
}

func generateUpfIdFromNodeId(nodeId *pfcpType.NodeID) (string, error) {
	switch nodeId.NodeIdType {
	case pfcpType.NodeIdTypeIpv4Address, pfcpType.NodeIdTypeIpv6Address:
		return net.IP(nodeId.NodeIdValue).String(), nil
	case pfcpType.NodeIdTypeFqdn:
		return string(nodeId.NodeIdValue), nil
	default:
		return "", fmt.Errorf("Invalid Node ID type: %v", nodeId.NodeIdType)
	}
}

func (upf *UPFInformation) GenerateTEID() uint32 {
	upf.TEIDCount++
	return upf.TEIDCount
}

func RetrieveUPFNodeByNodeId(nodeId pfcpType.NodeID) (upf *UPFInformation) {
	for _, upf := range upfPool {
		if reflect.DeepEqual(upf.NodeID, nodeId) {
			return upf
		}
	}
	return nil
}

func RemoveUPFNodeByNodeId(nodeId pfcpType.NodeID) {
	for upfID, upf := range upfPool {
		if reflect.DeepEqual(upf.NodeID, nodeId) {
			delete(upfPool, upfID)
			break
		}
	}
}

func SelectUPFByDnn(Dnn string) *UPFInformation {
	for _, upf := range upfPool {
		fmt.Println("[SMF] In SelectUPFByDnn UPFInfo.NetworkInstance: ", string(upf.UPIPInfo.NetworkInstance))
		if !upf.UPIPInfo.Assoni || string(upf.UPIPInfo.NetworkInstance) == Dnn {
			return upf
		}
	}

	fmt.Println("[SMF] ", upfPool)

	fmt.Println("[SMF] In SelectUPFByDnn")
	return nil
}

func (upf *UPFInformation) GetUPFIP() string {

	return upf.NodeID.ResolveNodeIdToIp().String()
}

func (upf *UPFInformation) pdrID() uint16 {

	if len(upf.pdrIdReuseQueue) == 0 {
		upf.pdrCount++
		return upf.pdrCount
	} else {
		pdrID := upf.pdrIdReuseQueue[0]
		upf.pdrIdReuseQueue = upf.pdrIdReuseQueue[1:]
		return pdrID
	}

}

func (upf *UPFInformation) farID() uint32 {

	if len(upf.farIdReuseQueue) == 0 {
		upf.farCount++
		return upf.farCount
	} else {
		farID := upf.farIdReuseQueue[0]
		upf.farIdReuseQueue = upf.farIdReuseQueue[1:]
		return farID
	}
}

func (upf *UPFInformation) barID() uint8 {

	if len(upf.barIdReuseQueue) == 0 {
		upf.barCount++
		return upf.barCount
	} else {
		barID := upf.barIdReuseQueue[0]
		upf.barIdReuseQueue = upf.barIdReuseQueue[1:]
		return barID
	}
}

func (upf *UPFInformation) AddPDR() (pdr *PDR) {
	pdr = new(PDR)
	pdr.PDRID = uint16(upf.pdrID())
	upf.pdrPool[pdr.PDRID] = pdr
	pdr.FAR = upf.AddFAR()
	return pdr
}

func (pdr *PDR) InitializePDR(smContext *SMContext) {

	tunnel := smContext.Tunnel
	pdr.State = RULE_INITIAL
	pdr.Precedence = 32
	pdr.PDI = PDI{
		SourceInterface: pfcpType.SourceInterface{
			InterfaceValue: pfcpType.SourceInterfaceAccess,
		},
		LocalFTeid: pfcpType.FTEID{
			V4:          true,
			Teid:        tunnel.ULTEID,
			Ipv4Address: tunnel.Node.UPIPInfo.Ipv4Address,
		},
		NetworkInstance: []byte(smContext.Dnn),
		UEIPAddress: &pfcpType.UEIPAddress{
			V4:          true,
			Ipv4Address: smContext.PDUAddress.To4(),
		},
	}
	pdr.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
	pdr.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4

	pdr.FAR.InitializeFAR(smContext)
}

func (upf *UPFInformation) AddFAR() (far *FAR) {
	far = new(FAR)
	far.FARID = upf.farID()
	upf.farPool[far.FARID] = far
	return far
}

func (far *FAR) InitializeFAR(smContext *SMContext) {

	far.ApplyAction.Forw = true
	far.ForwardingParameters = &ForwardingParameters{
		DestinationInterface: pfcpType.DestinationInterface{
			InterfaceValue: pfcpType.DestinationInterfaceCore,
		},
		NetworkInstance: []byte(smContext.Dnn),
	}
}

func (upf *UPFInformation) AddBAR() (bar *BAR) {
	bar = new(BAR)
	bar.BARID = uint8(upf.barID())
	upf.barPool[bar.BARID] = bar
	return bar
}

func (upf *UPFInformation) RemovePDR(pdr *PDR) {

	upf.RemovePDRID(pdr.PDRID)
	delete(upf.pdrPool, pdr.PDRID)
}

func (upf *UPFInformation) RemoveFAR(far *FAR) {

	upf.RemoveFARID(far.FARID)
	delete(upf.farPool, far.FARID)
}

func (upf *UPFInformation) RemoveBAR(bar *BAR) {

	upf.RemoveBARID(bar.BARID)
	delete(upf.barPool, bar.BARID)
}

func (upf *UPFInformation) RemovePDRID(PDRID uint16) {
	upf.pdrIdReuseQueue = append(upf.pdrIdReuseQueue, PDRID)
}

func (upf *UPFInformation) RemoveFARID(FARID uint32) {
	upf.farIdReuseQueue = append(upf.farIdReuseQueue, FARID)
}

func (upf *UPFInformation) RemoveBARID(BARID uint8) {
	upf.barIdReuseQueue = append(upf.barIdReuseQueue, BARID)
}

func (upf *UPFInformation) PrintPDRPoolStatus() {
	for k := range upf.pdrPool {
		fmt.Println("PDR ID: ", k, " using")
	}
}

func (upf *UPFInformation) PrintFARPoolStatus() {
	for k := range upf.farPool {
		fmt.Println("FAR ID: ", k, " using")
	}
}

func (upf *UPFInformation) PrintBARPoolStatus() {
	for k := range upf.barPool {
		fmt.Println("BAR ID: ", k, " using")
	}
}
