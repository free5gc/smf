package smf_context

import "fmt"

import "gofree5gc/src/smf/logger"

type BPManager struct {
	BPStatus   BPStatus
	ANUPFState map[*DataPathNode]bool
	PSAState   map[*DataPathNode]PDUSessionAnchorState

	//Need these variable conducting Add addtional PSA (TS23.502 4.3.5.4)
	//There value will change from time to time
	PSA1Path         []*UPNode
	PSA2Path         []*UPNode
	ULCL             *UPNode
	ULCLIdx          int
	ULCLDataPathNode *DataPathNode
	ULCLState        ULCLState
}

type BPStatus int

const (
	UnInitialized BPStatus = iota
	HasSendPFCPMsg
	InitializedSuccess
	InitializedFail
)

type PDUSessionAnchorState int

const (
	NotAdded PDUSessionAnchorState = iota
	AddPSASuccess
	AddPSAFail
)

type ULCLState int

const (
	IsOnlyULCL ULCLState = iota
	IsULCLAndPSA1
	IsULCLAndPSA2
)

func NewBPManager(supi string) (bpManager *BPManager) {
	ueRoutingGraph := SMF_Self().UERoutingGraphs[supi]

	bpManager = &BPManager{
		ANUPFState: ueRoutingGraph.ANUPF,
		PSAState:   make(map[*DataPathNode]PDUSessionAnchorState),
		PSA1Path:   make([]*UPNode, 0),
		ULCLState:  IsOnlyULCL,
	}

	for node, _ := range ueRoutingGraph.PSA {
		bpManager.PSAState[node] = NotAdded
	}

	return

}

func (bpMGR *BPManager) SetPSAStatus(psa_path []*UPNode) {

	if len(psa_path) == 0 {
		return
	}

	psa := psa_path[len(psa_path)-1]
	psa_ip := psa.NodeID.ResolveNodeIdToIp().String()

	for dataPathNode, _ := range bpMGR.PSAState {

		if psa_ip == dataPathNode.UPF.NodeID.ResolveNodeIdToIp().String() {
			bpMGR.PSAState[dataPathNode] = AddPSASuccess
			fmt.Println("Add PSA ", dataPathNode.UPF.GetUPFIP(), "Success")
			logger.PduSessLog.Traceln("Add PSA ", dataPathNode.UPF.GetUPFIP(), "Success")
			break
		}
	}

}

func (bpMGR *BPManager) SelectPSA2() {

	var psa2, curNode *DataPathNode
	psa2_path := make([]*UPNode, 0)
	upInfo := GetUserPlaneInformation()

	for dataPathNode, status := range bpMGR.PSAState {

		if status == NotAdded {
			psa2 = dataPathNode
			break
		}
	}

	for curNode = psa2; curNode != nil; curNode = curNode.Prev.To {

		curNodeIP := curNode.UPF.GetUPFIP()
		curUPNode := upInfo.GetUPFNodeByIP(curNodeIP)
		psa2_path = append([]*UPNode{curUPNode}, psa2_path...)
	}

	bpMGR.PSA2Path = psa2_path

	logger.PduSessLog.Traceln("SelectPSA2")
	for i, node := range psa2_path {

		logger.PduSessLog.Traceln("Node ", i, ": ", node.UPF.GetUPFIP())
	}
	return
}

func (bpMGR *BPManager) FindULCL(smContext *SMContext) (err error) {

	psa1_path := bpMGR.PSA1Path
	psa2_path := bpMGR.PSA2Path
	len_psa1_path := len(psa1_path)
	len_psa2_path := len(psa2_path)
	bpMGR.ULCL = nil
	bpMGR.ULCLDataPathNode = nil

	if len_psa1_path > len_psa2_path {

		for idx, node := range psa2_path {

			node1_id := psa1_path[idx].UPF.GetUPFID()
			node2_id := psa2_path[idx].UPF.GetUPFID()

			if node1_id == node2_id {
				bpMGR.ULCL = node
				bpMGR.ULCLIdx = idx
			} else {
				break
			}
		}
	} else {

		for idx, node := range psa1_path {

			node1_id := psa1_path[idx].UPF.GetUPFID()
			node2_id := psa2_path[idx].UPF.GetUPFID()

			if node1_id == node2_id {
				bpMGR.ULCL = node
				bpMGR.ULCLIdx = idx
			} else {
				break
			}
		}
	}

	if bpMGR.ULCL == nil {
		err = fmt.Errorf("Can't find ULCL!")
		return
	}

	upfRoot := smContext.Tunnel.UpfRoot
	upperBound := len(psa2_path) - 1

	curDataPathNode := upfRoot

	for idx, _ := range psa2_path {

		if idx == bpMGR.ULCLIdx {

			bpMGR.ULCLDataPathNode = curDataPathNode
			break
		}

		if idx < upperBound {
			nextUPFID := psa2_path[idx+1].UPF.GetUPFID()

			if nextDataPathLink, exist := curDataPathNode.Next[nextUPFID]; exist {

				curDataPathNode = nextDataPathLink.To
			} else {

				err = fmt.Errorf("PSA2 Path doesn't match UE Topo! error node: ", psa2_path[idx+1].UPF.GetUPFIP())
				return
			}
		}

	}

	if bpMGR.ULCLDataPathNode == nil {
		err = fmt.Errorf("Can't find ULCLDataPathNode!")
		return
	}

	logger.PduSessLog.Traceln("ULCL is : ", bpMGR.ULCLDataPathNode.UPF.GetUPFIP())

	return
}

// func (bpMGR *BPManager) EstablishPSA2(smContext *SMContext) {

// 	//upfRoot := smContext.Tunnel.UpfRoot
// 	psa2_path := bpMGR.PSA2Path

// 	curDataPathNode := bpMGR.ULCLDataPathNode
// 	upperBound := len(psa2_path) - 1

// 	for idx := bpMGR.ULCLIdx; idx <= upperBound; idx++ {

// 		if idx == bpMGR.ULCLIdx && idx == upperBound {
// 			//This upf is both ULCL and PSA2
// 			//So do nothing we will establish it ulcl rule later
// 			bpMGR.ULCLState = IsULCLAndPSA2
// 			break
// 		} else if idx == bpMGR.ULCLIdx {

// 			nextUPFID := psa2_path[idx+1].UPF.GetUPFID()
// 			curDataPathNode = curDataPathNode.Next[nextUPFID].To
// 		} else {

// 			SetUPPSA2Path(smContext, psa2_path[idx:], curDataPathNode)
// 		}

// 	}

// 	return
// }
