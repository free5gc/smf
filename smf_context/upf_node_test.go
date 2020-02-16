package smf_context_test

import (
	"fmt"
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/src/smf/smf_context"
	"testing"
)

var upf *smf_context.UPF
var pdrQueue []*smf_context.PDR
var farQueue []*smf_context.FAR
var barQueue []*smf_context.BAR

func init() {
	NodeID := new(pfcpType.NodeID)
	upf = smf_context.AddUPF(NodeID)
	pdrQueue = make([]*smf_context.PDR, 0)
	farQueue = make([]*smf_context.FAR, 0)
	barQueue = make([]*smf_context.BAR, 0)

	for i := 0; i < 6; i++ {
		pdrQueue = append(pdrQueue, upf.AddPDR())
		farQueue = append(farQueue, upf.AddFAR())
		barQueue = append(barQueue, upf.AddBAR())
	}

}

func TestRemovePDR(t *testing.T) {
	var exist bool

	// fmt.Println("Before Remove")
	// upf.PrintPDRPoolStatus()
	pdr := pdrQueue[0]
	upf.RemovePDR(pdr)
	exist = upf.CheckPDRIDExist(1)
	assertEqual(exist, false)

	pdr = pdrQueue[3]
	upf.RemovePDR(pdr)
	exist = upf.CheckPDRIDExist(4)
	assertEqual(exist, false)

	pdr = pdrQueue[5]
	upf.RemovePDR(pdr)
	exist = upf.CheckPDRIDExist(6)
	assertEqual(exist, false)

	// fmt.Println("After Remove")
	// upf.PrintPDRPoolStatus()

	//fmt.Println("Insert PDR")
	upf.AddPDR()
	//upf.PrintPDRPoolStatus()

	exist = upf.CheckPDRIDExist(1)
	assertEqual(exist, true)
}

func TestRemoveFAR(t *testing.T) {
	var exist bool

	//fmt.Println("Before Remove")
	//upf.PrintFARPoolStatus()
	far := farQueue[0]
	upf.RemoveFAR(far)

	exist = upf.CheckFARIDExist(2)
	assertEqual(exist, false)

	far = farQueue[3]
	upf.RemoveFAR(far)

	exist = upf.CheckFARIDExist(8)
	assertEqual(exist, false)
	far = farQueue[5]
	upf.RemoveFAR(far)

	exist = upf.CheckFARIDExist(12)
	assertEqual(exist, false)

	// fmt.Println("After Remove")
	// upf.PrintFARPoolStatus()

	//fmt.Println("Insert FAR")
	upf.AddFAR()
	//upf.PrintFARPoolStatus()

	exist = upf.CheckFARIDExist(2)
	assertEqual(exist, true)
}

func TestRemoveBAR(t *testing.T) {
	var exist bool

	// fmt.Println("Before Remove")
	// upf.PrintBARPoolStatus()
	bar := barQueue[0]
	upf.RemoveBAR(bar)
	exist = upf.CheckBARIDExist(1)
	assertEqual(exist, false)

	bar = barQueue[3]
	upf.RemoveBAR(bar)
	exist = upf.CheckBARIDExist(4)
	assertEqual(exist, false)

	bar = barQueue[5]
	upf.RemoveBAR(bar)
	exist = upf.CheckBARIDExist(6)
	assertEqual(exist, false)

	// fmt.Println("After Remove")
	// upf.PrintBARPoolStatus()

	fmt.Println("Insert BAR")
	upf.AddBAR()
	upf.AddBAR()
	exist = upf.CheckBARIDExist(1)
	assertEqual(exist, true)
	exist = upf.CheckBARIDExist(4)
	assertEqual(exist, true)
	//upf.PrintBARPoolStatus()
}

func assertEqual(a, b bool) {
	if a != b {
		panic(fmt.Sprintln("Not Equal: ", a, " ", b))
	}
}
