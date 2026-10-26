package context_test

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/context"
)

func TestSelectPSA2(t *testing.T) {
	const dnn = "internet"

	tests := []struct {
		name       string
		firstN3    bool
		anchorN9   bool
		anchorN3   bool
		singleNode bool
		wantSelect bool
	}{
		{
			name:       "N9-only PSA2",
			firstN3:    true,
			anchorN9:   true,
			wantSelect: true,
		},
		{
			name:       "PSA2 with N3 and N9",
			firstN3:    true,
			anchorN9:   true,
			anchorN3:   true,
			wantSelect: true,
		},
		{
			name:     "first UPF without N3",
			anchorN9: true,
		},
		{
			name:    "PSA2 without N9",
			firstN3: true,
		},
		{
			name:       "single-node PSA2 without N3",
			anchorN9:   true,
			singleNode: true,
		},
	}

	newUPF := func(n3, n9 bool) *context.UPF {
		upf := &context.UPF{}
		if n3 {
			upf.N3Interfaces = []*context.UPFInterfaceInfo{
				{NetworkInstances: []string{dnn}},
			}
		}
		if n9 {
			upf.N9Interfaces = []*context.UPFInterfaceInfo{
				{NetworkInstances: []string{dnn}},
			}
		}
		return upf
	}

	newPath := func(upfs ...*context.UPF) *context.DataPath {
		path := context.NewDataPath()
		var previous *context.DataPathNode

		for _, upf := range upfs {
			node := context.NewDataPathNode()
			node.UPF = upf
			if previous == nil {
				path.FirstDPNode = node
			} else {
				previous.AddNext(node)
				node.AddPrev(previous)
			}
			previous = node
		}
		return path
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ingress := newUPF(tt.firstN3, true)
			psa1 := newUPF(false, true)
			psa2 := newUPF(tt.anchorN3, tt.anchorN9)

			defaultPath := newPath(ingress, psa1)
			defaultPath.Activated = true

			var candidate *context.DataPath
			if tt.singleNode {
				candidate = newPath(psa2)
			} else {
				candidate = newPath(ingress, psa2)
			}

			sm := &context.SMContext{
				Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{
					Dnn: dnn,
				},
				Tunnel: &context.UPTunnel{
					DataPathPool: context.DataPathPool{
						1: defaultPath,
						2: candidate,
					},
				},
				Log: logrus.New().WithField("test", tt.name),
			}

			manager := context.NewBPManager("")
			manager.SelectPSA2(sm)

			if tt.wantSelect {
				require.Same(t, candidate, manager.ActivatingPath)
			} else {
				require.Nil(t, manager.ActivatingPath)
			}
			require.Equal(t, []*context.DataPath{defaultPath}, manager.ActivatedPaths)
		})
	}
}
