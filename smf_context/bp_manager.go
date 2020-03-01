package smf_context

type BPManager struct {
	ANUPFState map[*DataPathNode]bool
	PSAState   map[*DataPathNode]PDUSessionAnchorState
	PSA1Path   []*UPNode
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

func (bpMGR *BPManager) SelectPSA2() (psa2_path []*UPNode) {

	var psa2, curNode *DataPathNode
	psa2_path = make([]*UPNode, 0)
	upInfo := GetUserPlaneInformation()

	for dataPathNode, status := range bpMGR.PSAState {

		if status == NotAdded {
			psa2 = dataPathNode
			break
		}
	}

	for curNode = psa2; curNode != nil; curNode = psa2.Prev.To {

		curNodeIP := curNode.UPF.NodeID.ResolveNodeIdToIp().String()
		curUPNode := upInfo.GetUPFNodeByIP(curNodeIP)

		psa2_path = append([]*UPNode{curUPNode}, psa2_path...)
	}

	return
}

func (bpMGR *BPManager) FindULCL(psa1_path []*UPNode, psa2_path []*UPNode) (ulcl *UPNode) {

	len_psa1_path := len(psa1_path)
	len_psa2_path := len(psa2_path)
	ulcl = nil

	if len_psa1_path > len_psa2_path {

		for idx, node := range psa1_path {

			if psa1_path[idx] != psa2_path[idx] {
				break
			}

			ulcl = node
		}

	} else {

		for idx, node := range psa2_path {

			if psa1_path[idx] != psa2_path[idx] {
				break
			}

			ulcl = node
		}

	}

	return
}
