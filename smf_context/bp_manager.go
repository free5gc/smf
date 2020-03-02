package smf_context

type BPManager struct {
	ANUPFState map[*DataPathNode]bool
	PSAState   map[*DataPathNode]PDUSessionAnchorState

	//Need these variable conducting Add addtional PSA (TS23.502 4.3.5.4)
	//There value will change from time to time
	PSA1Path []*UPNode
	PSA2Path []*UPNode
	ULCL     *UPNode
	ULCLIdx  int
}

type PDUSessionAnchorState int

const (
	NotAdded       PDUSessionAnchorState = 0
	HasSendPFCPMsg PDUSessionAnchorState = 1
	AddPSASuccess  PDUSessionAnchorState = 2
	AddPSAFail     PDUSessionAnchorState = 3
)

func NewBPManager(supi string) (bpManager *BPManager) {
	ueRoutingGraph := SMF_Self().UERoutingGraphs[supi]

	bpManager = &BPManager{
		ANUPFState: ueRoutingGraph.ANUPF,
		PSAState:   make(map[*DataPathNode]PDUSessionAnchorState),
		PSA1Path:   make([]*UPNode, 0),
	}

	for node, _ := range ueRoutingGraph.PSA {
		bpManager.PSAState[node] = NotAdded
	}

	return

}

func (bpMGR *BPManager) SetPSAStatus(psa_path []*UPNode) {

	psa := psa_path[len(psa_path)-1]
	psa_ip := psa.NodeID.ResolveNodeIdToIp().String()

	for dataPathNode, _ := range bpMGR.PSAState {

		if psa_ip == dataPathNode.UPF.NodeID.ResolveNodeIdToIp().String() {
			bpMGR.PSAState[dataPathNode] = AddPSASuccess
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

	for curNode = psa2; curNode != nil; curNode = psa2.Prev.To {

		curNodeIP := curNode.UPF.GetUPFIP()
		curUPNode := upInfo.GetUPFNodeByIP(curNodeIP)
		psa2_path = append([]*UPNode{curUPNode}, psa2_path...)
	}

	bpMGR.PSA2Path = psa2_path
	return
}

func (bpMGR *BPManager) FindULCL() {

	psa1_path := bpMGR.PSA1Path
	psa2_path := bpMGR.PSA2Path
	len_psa1_path := len(psa1_path)
	len_psa2_path := len(psa2_path)
	bpMGR.ULCL = nil

	if len_psa1_path > len_psa2_path {

		for idx, node := range psa1_path {

			node1_ip := psa1_path[idx].UPF.GetUPFIP()
			node2_ip := psa2_path[idx].UPF.GetUPFIP()

			if node1_ip == node2_ip {
				bpMGR.ULCL = node
				bpMGR.ULCLIdx = idx
			} else {
				break
			}

		}

	} else {

		for idx, node := range psa2_path {

			node1_ip := psa1_path[idx].UPF.GetUPFIP()
			node2_ip := psa2_path[idx].UPF.GetUPFIP()

			if node1_ip == node2_ip {
				bpMGR.ULCL = node
				bpMGR.ULCLIdx = idx
			} else {
				break
			}
		}

	}

	return
}

func (bpMGR *BPManager) EstablishPSA2(smContext *SMContext) {

	upfRoot := smContext.Tunnel.UpfRoot
}
