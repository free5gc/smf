package smf_producer_test

import (
	"free5gc/lib/path_util"
	"free5gc/src/smf/context"
	"free5gc/src/smf/factory"
	"free5gc/src/smf/pfcp/udp"
	"free5gc/src/smf/smf_producer"
	"net"
	"testing"
)

func init() {

	//config path
	DefaultSmfConfigPath := path_util.Gofree5gcPath("free5gc/config/smf.FR5GC858.cfg")
	factory.InitConfigFactory(DefaultSmfConfigPath)

	//read config to data structure
	context.InitSmfContext(&factory.SmfConfig)
	context.AllocateUPFID()
	userPlaneInfo := context.GetUserPlaneInformation()
	for node_name, node := range userPlaneInfo.UPNodes {

		if node_name == "AnchorUPF3" {
			node.UPF.UPIPInfo.NetworkInstance = []byte("internet")
			break
		}
	}

	userPlaneInfo.GenerateDefaultPath("internet")
	udp.Run()
}

func TestSetUpUplinkUserPlane(t *testing.T) {

	upfRoot := context.GetUserPlaneInformation().GetDefaultUPFTopoByDNN("internet")
	smContext := context.NewSMContext("imsi-2089300007487", 20)
	smContext.PDUAddress = net.ParseIP("60.60.0.1")
	smContext.Dnn = "internet"
	SetUpAllUPF(upfRoot)
	smf_producer.SetUpUplinkUserPlane(upfRoot, smContext)
}

func TestSetUpDownlinkUserPlane(t *testing.T) {

	upfRoot := context.GetUserPlaneInformation().GetDefaultUPFTopoByDNN("internet")
	smContext := context.NewSMContext("imsi-2089300007487", 20)
	smContext.PDUAddress = net.ParseIP("60.60.0.1")
	smContext.Dnn = "internet"
	SetUpAllUPF(upfRoot)
	smf_producer.SetUpDownLinkUserPlane(upfRoot, smContext)
}

func SetUpAllUPF(node *context.DataPathNode) {

	node.UPF.UPFStatus = context.AssociatedSetUpSuccess
	node.UPF.UPIPInfo.Ipv4Address = net.ParseIP("10.200.200.50").To4()

	for _, child_link := range node.DataPathToDN {

		SetUpAllUPF(child_link.To)
	}
}
