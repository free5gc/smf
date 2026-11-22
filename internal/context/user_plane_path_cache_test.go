package context_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

func newPathCacheTopology(t *testing.T) (*smf_context.UserPlaneInformation, []*smf_context.UPFSelectionParams) {
	t.Helper()
	selections := make([]*smf_context.UPFSelectionParams, 8)
	dnns := make([]*factory.DnnUpfInfoItem, len(selections))
	for i := range selections {
		dnn := fmt.Sprintf("internet%d", i)
		selections[i] = &smf_context.UPFSelectionParams{
			Dnn: dnn, SNssai: &smf_context.SNssai{Sst: 1, Sd: "010203"},
		}
		dnns[i] = &factory.DnnUpfInfoItem{Dnn: dnn}
	}
	topologyConfig := &factory.UserPlaneInformation{
		UPNodes: map[string]*factory.UPNode{"AN": {Type: "AN"}},
	}
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("UPF%d", i)
		topologyConfig.UPNodes[name] = &factory.UPNode{
			Type: "UPF", NodeID: fmt.Sprintf("192.0.2.%d", i),
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{{
				SNssai: &models.Snssai{Sst: 1, Sd: "010203"}, DnnUpfInfoList: dnns,
			}},
		}
		topologyConfig.Links = append(topologyConfig.Links, &factory.UPLink{A: "AN", B: name})
	}
	upi, err := smf_context.NewUserPlaneInformation(topologyConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, upf := range upi.UPFs {
			smf_context.RemoveUPFNodeByNodeID(upf.NodeID)
		}
	})
	return upi, selections
}

func TestConcurrentDefaultPathCache(t *testing.T) {
	for _, sharedSelection := range []bool{true, false} {
		t.Run(fmt.Sprintf("sharedSelection=%t", sharedSelection), func(t *testing.T) {
			upi, selections := newPathCacheTopology(t)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for worker := 0; worker < 64; worker++ {
				wg.Go(func() {
					selection := selections[0]
					if !sharedSelection {
						selection = selections[worker%len(selections)]
					}
					upf := upi.UPFs[fmt.Sprintf("UPF%d", worker%2+1)]
					<-start
					for iteration := 0; iteration < 32; iteration++ {
						// Session handlers may concurrently hold the topology read lock.
						upi.Mu.RLock()
						switch (worker + iteration) % 4 {
						case 0:
							path := upi.GetDefaultUserPlanePathByDNNAndUPF(selection, upf)
							if len(path) != 1 || path[0] != upf {
								t.Errorf("unexpected path to %s: %v", upf.Name, path)
							}
						case 1:
							path := upi.GetDefaultUserPlanePathByDNN(selection)
							if len(path) != 1 || !path[0].MatchedSelection(selection) {
								t.Errorf("unexpected default path: %v", path)
							}
						case 2:
							if !upi.GenerateDefaultPathToUPF(selection, upf) {
								t.Error("failed to generate path to UPF")
							}
						case 3:
							if !upi.GenerateDefaultPath(selection) || !upi.ExistDefaultPath(selection.String()) {
								t.Error("failed to cache default path")
							}
						}
						upi.Mu.RUnlock()
					}
				})
			}
			close(start)
			wg.Wait()
		})
	}
}

func TestDefaultPathCacheInvalidation(t *testing.T) {
	upi, selections := newPathCacheTopology(t)
	selection := selections[0]
	removed, retained := upi.UPFs["UPF1"], upi.UPFs["UPF2"]
	for _, upf := range []*smf_context.UPNode{removed, retained} {
		require.Equal(t, smf_context.UPPath{upf}, upi.GetDefaultUserPlanePathByDNNAndUPF(selection, upf))
	}
	upi.Mu.Lock()
	upi.UpNodeDelete(removed.Name)
	upi.Mu.Unlock()
	cache := upi.DefaultUserPlanePathToUPF[selection.String()]
	require.NotContains(t, cache, removed.NodeID.ResolveNodeIdToIp().String())
	require.Contains(t, cache, retained.NodeID.ResolveNodeIdToIp().String())
	require.Nil(t, upi.GetDefaultUserPlanePathByDNNAndUPF(selection, removed))
	require.Equal(t, smf_context.UPPath{retained}, upi.GetDefaultUserPlanePathByDNNAndUPF(selection, retained))
}
