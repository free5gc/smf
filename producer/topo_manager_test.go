package producer_test

import (
	"free5gc/lib/path_util"
	"free5gc/src/smf/context"
	"free5gc/src/smf/factory"
	"free5gc/src/smf/pfcp/udp"
	"free5gc/src/smf/producer"
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
