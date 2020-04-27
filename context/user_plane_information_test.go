package context_test

import (
	"free5gc/lib/path_util"
	"free5gc/src/smf/context"
	"free5gc/src/smf/factory"
	"testing"
)

func init() {

	//config path
	DefaultSmfConfigPath := path_util.Gofree5gcPath("free5gc/config/smf.FR5GC858.cfg")
	factory.InitConfigFactory(DefaultSmfConfigPath)

	//read config to data structure
	context.InitSmfContext(&factory.SmfConfig)
	context.AllocateUPFID()
}

func TestGenerateDefaultPath(t *testing.T) {

	userPlaneInfo := context.GetUserPlaneInformation()

	for node_name, node := range userPlaneInfo.UPNodes {

		if node_name == "AnchorUPF3" {
			node.UPF.UPIPInfo.NetworkInstance = []byte("internet")
			break
		}
	}

	//userPlaneInfo.PrintUserPlaneTopology()
	pathExist := userPlaneInfo.GenerateDefaultPath("internet")
	assertEqual(pathExist, true)
}

func TestGetDefaultUPFTopoByDNN(t *testing.T) {

	userPlaneInfo := context.GetUserPlaneInformation()

	for node_name, node := range userPlaneInfo.UPNodes {

		if node_name == "AnchorUPF3" {
			node.UPF.UPIPInfo.NetworkInstance = []byte("internet")
			break
		}
	}

	//userPlaneInfo.PrintUserPlaneTopology()
	userPlaneInfo.GenerateDefaultPath("internet")
	//userPlaneInfo.PrintDefaultDnnPath("internet")
	root := userPlaneInfo.GetDefaultUPFTopoByDNN("internet")

	if root == nil {
		panic("There is no default upf topo")
	}
}
