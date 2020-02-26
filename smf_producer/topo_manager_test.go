package smf_producer_test

import (
	"fmt"
	"gofree5gc/lib/path_util"
	"gofree5gc/src/smf/factory"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_pfcp/pfcp_udp"
	"gofree5gc/src/smf/smf_producer"
	"net"
	"testing"
)

func init() {

	//config path
	DefaultSmfConfigPath := path_util.Gofree5gcPath("gofree5gc/config/smf.FR5GC858.cfg")
	factory.InitConfigFactory(DefaultSmfConfigPath)

	//read config to data structure
	smf_context.InitSmfContext(&factory.SmfConfig)
	smf_context.AllocateUPFID()
	userPlaneInfo := smf_context.GetUserPlaneInformation()
	for node_name, node := range userPlaneInfo.UPNodes {

		if node_name == "AnchorUPF3" {
			node.UPF.UPIPInfo.NetworkInstance = []byte("internet")
			break
		}
	}

	userPlaneInfo.GenerateDefaultPath("internet")
	userPlaneInfo.PrintDefaultDnnPath("internet")

	test_map := make(map[int]bool)
	fmt.Println(test_map[2])
	//smfContext := smf_context.SMF_Self()

	// smfContext.CPNodeID.NodeIdType = 0
	// smfContext.CPNodeID.NodeIdValue = net.ParseIP("127.0.0.1").To4()

	pfcp_udp.Run()
}

func TestSetUpUplinkUserPlane(t *testing.T) {

	upfRoot := smf_context.GetUserPlaneInformation().GetDefaultUPFTopoByDNN("internet")
	smContext := smf_context.NewSMContext("imsi-2089300007487", 20)
	smContext.PDUAddress = net.ParseIP("60.60.0.1")
	smContext.Dnn = "internet"
	SetUpAllUPF(upfRoot)
	smf_producer.SetUpUplinkUserPlane(upfRoot, smContext)
}

func SetUpAllUPF(node *smf_context.DataPathNode) {

	node.UPF.UPFStatus = smf_context.AssociatedSetUpSuccess

	for _, child_link := range node.Next {

		SetUpAllUPF(child_link.To)
	}
}
