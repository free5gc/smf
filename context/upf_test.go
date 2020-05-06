package context_test

import (
	"free5gc/lib/pfcp/pfcpType"
	"free5gc/src/smf/context"
)

var upf *context.UPF
var pdrQueue []*context.PDR
var farQueue []*context.FAR
var barQueue []*context.BAR

func init() {
	NodeID := new(pfcpType.NodeID)
	upf = context.NewUPF(NodeID)
	pdrQueue = make([]*context.PDR, 0)
	farQueue = make([]*context.FAR, 0)
	barQueue = make([]*context.BAR, 0)

	for i := 0; i < 6; i++ {
		pdr, _ := upf.AddPDR()
		far, _ := upf.AddFAR()
		bar, _ := upf.AddBAR()

		pdrQueue = append(pdrQueue, pdr)
		farQueue = append(farQueue, far)
		barQueue = append(barQueue, bar)
	}

}
