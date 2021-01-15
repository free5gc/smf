package context_test

import (
	"free5gc/src/smf/context"
	"free5gc/src/smf/factory"
	"testing"

	"github.com/stretchr/testify/require"
)

var configuration = &factory.UserPlaneInformation{
	UPNodes: map[string]factory.UPNode{
		"GNodeB": {
			Type:   "AN",
			NodeID: "192.168.179.100",
		},
		"UPF1": {
			Type:   "UPF",
			NodeID: "192.168.179.1",
		},
		"UPF2": {
			Type:   "UPF",
			NodeID: "192.168.179.2",
		},
		"UPF3": {
			Type:   "UPF",
			NodeID: "192.168.179.3",
		},
		"UPF4": {
			Type:   "UPF",
			NodeID: "192.168.179.4",
		},
	},
	Links: []factory.UPLink{
		{
			A: "GNodeB",
			B: "UPF1",
		},
		{
			A: "UPF1",
			B: "UPF2",
		},
		{
			A: "UPF2",
			B: "UPF3",
		},
		{
			A: "UPF3",
			B: "UPF4",
		},
	},
}

func TestNewUserPlaneInformation(t *testing.T) {
	userplaneInformation := context.NewUserPlaneInformation(configuration)

	require.NotNil(t, userplaneInformation.AccessNetwork["GNodeB"])

	require.NotNil(t, userplaneInformation.UPFs["UPF1"])
	require.NotNil(t, userplaneInformation.UPFs["UPF2"])
	require.NotNil(t, userplaneInformation.UPFs["UPF3"])
	require.NotNil(t, userplaneInformation.UPFs["UPF4"])

	// check links
	require.Contains(t, userplaneInformation.AccessNetwork["GNodeB"].Links, userplaneInformation.UPFs["UPF1"])
	require.Contains(t, userplaneInformation.UPFs["UPF1"].Links, userplaneInformation.UPFs["UPF2"])
	require.Contains(t, userplaneInformation.UPFs["UPF2"].Links, userplaneInformation.UPFs["UPF3"])
	require.Contains(t, userplaneInformation.UPFs["UPF3"].Links, userplaneInformation.UPFs["UPF4"])

}

func TestGenerateDefaultPath(t *testing.T) {
}

func TestGetDefaultUPFTopoByDNN(t *testing.T) {
}
