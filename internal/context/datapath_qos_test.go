package context_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

func TestPathActivationUsesExplicitQoSBinding(t *testing.T) {
	for _, activation := range []struct {
		name string
		run  func(*smf_context.DataPath, *smf_context.SMContext, uint32, ...smf_context.PathQoSBinding) error
	}{
		{"normal", (*smf_context.DataPath).ActivateTunnelAndPDR},
		{"DC", (*smf_context.DataPath).ActivateDcTunnelAndPDR},
	} {
		t.Run(activation.name, func(t *testing.T) {
			for _, tc := range []struct {
				name         string
				defaultRoute bool
				qfi          uint8
				qos          *models.Pcf_SMPolCtrl_QosData
				gbr          bool
			}{
				{name: "default flow on another route", qfi: 7},
				{
					name: "dedicated flow on default route", defaultRoute: true, qfi: 4,
					qos: &models.Pcf_SMPolCtrl_QosData{Var5qi: 9, MaxbrUl: "10 Mbps", MaxbrDl: "20 Mbps"},
				},
				{
					name: "GBR flow", qfi: 4, gbr: true,
					qos: &models.Pcf_SMPolCtrl_QosData{
						Var5qi: 1, GbrUl: "5 Mbps", GbrDl: "5 Mbps", MaxbrUl: "10 Mbps", MaxbrDl: "20 Mbps",
					},
				},
				{
					name: "default indication ignores other QoS fields", qfi: 7,
					qos: &models.Pcf_SMPolCtrl_QosData{
						DefQosFlowIndication: true, Var5qi: 1, GbrUl: "unused", MaxbrUl: "unused",
					},
				},
				{name: "dedicated flow without optional MBR", qfi: 4, qos: &models.Pcf_SMPolCtrl_QosData{Var5qi: 9}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					sm := newTIPolicyContext(t)
					sm.SelectedSessionRule().DefQosQFI = 7
					installDefault(t, sm)
					dp := &smf_context.DataPath{
						IsDefaultPath: tc.defaultRoute,
						FirstDPNode:   sm.Tunnel.DataPathPool.GetDefaultPath().CopyFirstDPNode(),
					}
					require.NoError(t, activation.run(dp, sm, 100, smf_context.PathQoSBinding{QFI: tc.qfi, QosData: tc.qos}))
					require.True(t, dp.Activated)
					require.Equal(t, tc.gbr, dp.GBRFlow)
					assertMarkingQFI(t, dp, tc.qfi)
					for node := dp.FirstDPNode; node != nil; node = node.Next() {
						for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
							ambrCount := 0
							for _, qer := range tunnel.PDR.QER {
								if qer.QFI == nil {
									ambrCount++
									require.NotNil(t, qer.MBR)
									continue
								}
								if tc.gbr {
									require.NotNil(t, qer.GBR)
									require.NotNil(t, qer.MBR)
								} else if tc.qfi == 7 || tc.qos.MaxbrUl == "" {
									require.Nil(t, qer.MBR)
									require.Nil(t, qer.GBR)
								} else {
									require.Equal(t, uint64(10000), qer.MBR.ULMBR)
									require.Equal(t, uint64(20000), qer.MBR.DLMBR)
								}
							}
							if tc.gbr {
								require.Zero(t, ambrCount)
							} else {
								require.Equal(t, 1, ambrCount)
							}
						}
					}
				})
			}
		})
	}
}

func TestPathActivationRejectsInvalidQoSBindingBeforeAllocation(t *testing.T) {
	for _, tc := range []struct {
		name string
		qfi  uint8
		qos  *models.Pcf_SMPolCtrl_QosData
	}{
		{name: "zero QFI"},
		{name: "out of range QFI", qfi: 64},
		{name: "missing dedicated QoS", qfi: 4},
		{name: "mismatched default indication", qfi: 4, qos: &models.Pcf_SMPolCtrl_QosData{DefQosFlowIndication: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := newTIPolicyContext(t)
			installDefault(t, sm)
			for _, activate := range []func(
				*smf_context.DataPath, *smf_context.SMContext, uint32, ...smf_context.PathQoSBinding,
			) error{
				(*smf_context.DataPath).ActivateTunnelAndPDR,
				(*smf_context.DataPath).ActivateDcTunnelAndPDR,
			} {
				dp := &smf_context.DataPath{FirstDPNode: sm.Tunnel.DataPathPool.GetDefaultPath().CopyFirstDPNode()}
				require.Error(t, activate(dp, sm, 100, smf_context.PathQoSBinding{QFI: tc.qfi, QosData: tc.qos}))
				require.False(t, dp.Activated)
				require.Nil(t, dp.FirstDPNode.UpLinkTunnel.PDR)
			}
		})
	}
}
