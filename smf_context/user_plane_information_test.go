package smf_context_test

import (
	"fmt"
	"gofree5gc/lib/path_util"
	"gofree5gc/src/smf/factory"
	"gofree5gc/src/smf/smf_context"
	"testing"
)

func init() {

	//config path
	DefaultSmfConfigPath := path_util.Gofree5gcPath("gofree5gc/config/smf.FR5GC858.cfg")
	factory.InitConfigFactory(DefaultSmfConfigPath)

	//read config to data structure
	smf_context.InitSmfContext(&factory.SmfConfig)
	smf_context.AllocateUPFID()
}

func TestGenerateDefaultPath(t *testing.T) {

	userPlaneInfo := smf_context.GetUserPlaneInformation()

	for node_name, node := range userPlaneInfo.UPNodes {

		if node_name == "AnchorUPF3" {
			node.UPF.UPIPInfo.NetworkInstance = []byte("internet")
			break
		}
	}

	//userPlaneInfo.PrintUserPlaneTopology()
	path, pathExist := userPlaneInfo.GenerateDefaultPath("internet")

	assertEqual(pathExist, true)
	for idx, node := range path {

		if node.Type == smf_context.UPNODE_AN {
			fmt.Println("Node ", idx, ": ", node.ANIP.String())
		} else if node.Type == smf_context.UPNODE_UPF {
			fmt.Println("Node ", idx, ": ", node.NodeID.ResolveNodeIdToIp().String())
		}
	}

}
