package smf_context

import (
	"fmt"
	"gofree5gc/lib/pfcp/pfcpType"
	"net"
	"reflect"
)

var upfPool map[string]*UPF

func init() {
	upfPool = make(map[string]*UPF)
}

type UPTunnel struct {
	Node  *UPF
	ULPDR *PDR
	DLPDR *PDR

	ULTEID uint32
	DLTEID uint32
}

type UPFStatus int

const (
	NotAssociated          UPFStatus = 0
	AssociatedSettingUp    UPFStatus = 1
	AssociatedSetUpSuccess UPFStatus = 2
)

type UPF struct {
	NodeID    pfcpType.NodeID
	UPIPInfo  pfcpType.UserPlaneIPResourceInformation
	UPFStatus UPFStatus

	pdrPool         map[uint16]*PDR
	farPool         map[uint32]*FAR
	barPool         map[uint8]*BAR
	urrPool         map[uint32]*URR
	qerPool         map[uint32]*QER
	teidPool        map[uint32]bool
	pdrCount        uint16
	farCount        uint32
	barCount        uint8
	urrCount        uint32
	qerCount        uint32
	TEIDCount       uint32
	pdrIdReuseQueue *IDQueue
	farIdReuseQueue *IDQueue
	barIdReuseQueue *IDQueue
}

func AddUPF(nodeId *pfcpType.NodeID) (upf *UPF) {
	upf = new(UPF)
	key, err := generateUpfIdFromNodeId(nodeId)

	if err != nil {
		fmt.Println("[SMF] Error occurs while calling AddUPF")
		return
	}

	upfPool[key] = upf
	upf.UPFStatus = NotAssociated
	upf.NodeID = *nodeId
	upf.pdrPool = make(map[uint16]*PDR)
	upf.farPool = make(map[uint32]*FAR)
	upf.barPool = make(map[uint8]*BAR)
	upf.qerPool = make(map[uint32]*QER)
	upf.urrPool = make(map[uint32]*URR)
	upf.teidPool = make(map[uint32]bool)
	upf.pdrIdReuseQueue = NewIDQueue(PDRType)
	upf.farIdReuseQueue = NewIDQueue(FARType)
	upf.barIdReuseQueue = NewIDQueue(BARType)

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

func (upf *UPF) GenerateTEID() (id uint32, err error) {
	if upf.UPFStatus != AssociatedSetUpSuccess {
		err := fmt.Errorf("UPF didn't Setup!")
		return 0, err
	}
	id = uint32(upf.GetValidID(TEIDType))
	upf.teidPool[id] = true
	return
}

func RetrieveUPFNodeByNodeId(nodeId pfcpType.NodeID) (upf *UPF) {

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

func SelectUPFByDnn(Dnn string) *UPF {
	for _, upf := range upfPool {
		if !upf.UPIPInfo.Assoni || string(upf.UPIPInfo.NetworkInstance) == Dnn {
			return upf
		}
	}
	return nil
}

func (upf *UPF) GetUPFIP() string {

	return upf.NodeID.ResolveNodeIdToIp().String()
}

func (upf *UPF) pdrID() (pdrID uint16, err error) {
	if upf.UPFStatus != AssociatedSetUpSuccess {
		err := fmt.Errorf("UPF didn't Setup!")
		return 0, err
	}

	if upf.pdrIdReuseQueue.IsEmpty() {
		id := upf.GetValidID(PDRType)
		pdrID = uint16(id)
	} else {
		id, err := upf.pdrIdReuseQueue.Pop()

		if err != nil {
			fmt.Println(err)
		}

		pdrID = uint16(id)
	}

	return
}

func (upf *UPF) farID() (farID uint32, err error) {
	if upf.UPFStatus != AssociatedSetUpSuccess {
		err := fmt.Errorf("UPF didn't Setup!")
		return 0, err
	}

	if upf.farIdReuseQueue.IsEmpty() {

		id := upf.GetValidID(FARType)
		farID = uint32(id)
	} else {
		id, err := upf.farIdReuseQueue.Pop()

		if err != nil {
			fmt.Println(err)
		}
		farID = uint32(id)
	}

	return
}

func (upf *UPF) barID() (barID uint8, err error) {
	if upf.UPFStatus != AssociatedSetUpSuccess {
		err := fmt.Errorf("UPF didn't Setup!")
		return 0, err
	}

	if upf.barIdReuseQueue.IsEmpty() {

		id := upf.GetValidID(BARType)
		barID = uint8(id)
	} else {
		id, err := upf.barIdReuseQueue.Pop()

		if err != nil {
			fmt.Println(err)
		}
		barID = uint8(id)
	}

	return
}

func (upf *UPF) AddPDR() (pdr *PDR, err error) {

	if upf.UPFStatus != AssociatedSetUpSuccess {
		err = fmt.Errorf("UPF didn't Setup!")
		return nil, err
	}

	pdr = new(PDR)
	PDRID, _ := upf.pdrID()
	pdr.PDRID = uint16(PDRID)
	upf.pdrPool[pdr.PDRID] = pdr
	pdr.FAR, _ = upf.AddFAR()
	return pdr, nil
}

func (upf *UPF) AddFAR() (far *FAR, err error) {

	if upf.UPFStatus != AssociatedSetUpSuccess {
		err = fmt.Errorf("UPF didn't Setup!")
		return nil, err
	}

	far = new(FAR)
	far.FARID, _ = upf.farID()
	upf.farPool[far.FARID] = far
	return far, nil
}

func (upf *UPF) AddBAR() (bar *BAR, err error) {

	if upf.UPFStatus != AssociatedSetUpSuccess {
		err = fmt.Errorf("UPF didn't Setup!")
		return nil, err
	}

	bar = new(BAR)
	BARID, _ := upf.barID()
	bar.BARID = uint8(BARID)
	upf.barPool[bar.BARID] = bar
	return bar, nil
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

func (far *FAR) InitializeFAR(smContext *SMContext) {

	far.ApplyAction.Forw = true
	far.ForwardingParameters = &ForwardingParameters{
		DestinationInterface: pfcpType.DestinationInterface{
			InterfaceValue: pfcpType.DestinationInterfaceCore,
		},
		NetworkInstance: []byte(smContext.Dnn),
	}
}

func (upf *UPF) RemovePDR(pdr *PDR) (err error) {

	if upf.UPFStatus != AssociatedSetUpSuccess {
		err = fmt.Errorf("UPF didn't Setup!")
		return err
	}

	upf.pdrIdReuseQueue.Push(int(pdr.PDRID))
	delete(upf.pdrPool, pdr.PDRID)
	return nil
}

func (upf *UPF) RemoveFAR(far *FAR) (err error) {

	upf.farIdReuseQueue.Push(int(far.FARID))
	delete(upf.farPool, far.FARID)
	return nil
}

func (upf *UPF) RemoveBAR(bar *BAR) (err error) {

	if upf.UPFStatus != AssociatedSetUpSuccess {
		err = fmt.Errorf("UPF didn't Setup!")
		return err
	}

	upf.barIdReuseQueue.Push(int(bar.BARID))
	delete(upf.barPool, bar.BARID)
	return nil
}

func (upf *UPF) GetValidID(idType IDType) (id int) {

	switch idType {
	case PDRType:
		for {
			upf.pdrCount++
			if _, exist := upf.pdrPool[upf.pdrCount]; !exist { // valid id
				break
			}
		}

		id = int(upf.pdrCount)
	case FARType:
		for {
			upf.farCount++
			if _, exist := upf.farPool[upf.farCount]; !exist { // valid id
				break
			}
		}

		id = int(upf.farCount)
	case BARType:
		for {
			upf.barCount++
			if _, exist := upf.barPool[upf.barCount]; !exist { // valid id
				break
			}
		}

		id = int(upf.barCount)
	case TEIDType:
		for {
			upf.TEIDCount++
			if _, exist := upf.teidPool[upf.TEIDCount]; !exist { // valid id
				break
			}
		}

		id = int(upf.TEIDCount)
	}
	return
}

func (upf *UPF) PrintPDRPoolStatus() {
	for k := range upf.pdrPool {
		fmt.Println("PDR ID: ", k, " using")
	}
}

func (upf *UPF) PrintFARPoolStatus() {
	for k := range upf.farPool {
		fmt.Println("FAR ID: ", k, " using")
	}
}

func (upf *UPF) PrintBARPoolStatus() {
	for k := range upf.barPool {
		fmt.Println("BAR ID: ", k, " using")
	}
}

func (upf *UPF) CheckPDRIDExist(id int) (exist bool) {
	_, exist = upf.pdrPool[uint16(id)]
	return
}

func (upf *UPF) CheckFARIDExist(id int) (exist bool) {
	_, exist = upf.farPool[uint32(id)]
	return
}

func (upf *UPF) CheckBARIDExist(id int) (exist bool) {
	_, exist = upf.barPool[uint8(id)]
	return
}
