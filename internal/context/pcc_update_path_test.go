package context_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

const pccUpdateDefaultFlow = "permit out ip from any to assigned"

// TS 29.512 V17.11.0 clause 4.2.4.15: a failed modification must
// retain the active old rule when the failure does not affect that rule.
func TestPCCUpdatePathReplacement(t *testing.T) {
	for _, level := range []string{"session charging", "flow charging"} {
		for _, explicit := range []bool{true, false} {
			for _, reachable := range []bool{false, true} {
				name := level + "/referenced data"
				if explicit {
					name = level + "/explicit rule"
				}
				if reachable {
					name += "/successful replacement"
				} else {
					name += "/unavailable replacement route"
				}
				t.Run(name, func(t *testing.T) {
					sm := newTIPolicyContext(t)
					sm.UrrReportThreshold = 3000
					rule := tiPCC("charged", "", "route", 255)
					if level == "session charging" {
						rule.FlowInfos[0].FlowDescription = pccUpdateDefaultFlow
					}
					rule.RefChgData = []string{"charging"}
					initial := &models.Pcf_SMPolCtrl_SmPolicyDecision{
						PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"charged": rule},
						TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{
							"route": {TcId: "route"},
						},
						ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{
							"charging": {
								ChgId: "charging", RatingGroup: 42, Offline: true,
								MeteringMethod: models.Pcf_SMPolCtrl_MeteringMethod_VOLUME,
								ReportingLevel: models.Pcf_SMPolCtrl_ReportingLevel_RAT_GR_LEVEL,
							},
						},
					}
					require.NoError(t, sm.ApplyPccRules(initial))
					old := sm.PCCRules["charged"]
					oldPath := old.Datapath
					oldCharging := oldPath.GetChargingUrr(sm)
					require.Len(t, oldCharging, 1)
					oldInfo := sm.ChargingInfo[oldCharging[0].URRID]
					oldData := sm.ChargingData["charging"]
					oldTC := sm.TrafficControlDatas["route"]
					for node := oldPath.FirstDPNode; node != nil; node = node.Next() {
						for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
							tunnel.PDR.State = smf_context.RULE_CREATE
							tunnel.PDR.FAR.State = smf_context.RULE_CREATE
						}
					}

					// The new route has no matching UPF; the old route remains usable.
					// Exercise the real allocation failure without altering cleanup code.
					dnai := "unavailable"
					if reachable {
						dnai = "mec"
					}
					update := &models.Pcf_SMPolCtrl_SmPolicyDecision{
						TraffContDecs: map[string]*models.Pcf_SMPolCtrl_TrafficControlData{
							"route": {TcId: "route", RouteToLocs: []models.RouteToLocation{{Dnai: dnai}}},
						},
					}
					if explicit {
						model := *rule
						update.PccRules = map[string]*models.Pcf_SMPolCtrl_PccRule{"charged": &model}
					}
					err := sm.ApplyPccRules(update)
					if !reachable {
						require.EqualError(t, err, "fail to create data path for pcc rule[charged]")
						require.Same(t, old, sm.PCCRules["charged"])
						require.Same(t, oldPath, old.Datapath)
						require.NotContains(t, sm.DataPathToBeRemoved, oldPath.PathID)
						for node := oldPath.FirstDPNode; node != nil; node = node.Next() {
							for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
								require.Equal(t, smf_context.RULE_CREATE, tunnel.PDR.State)
								require.Equal(t, smf_context.RULE_CREATE, tunnel.PDR.FAR.State)
							}
						}
						sm.PostRemoveDataPath()
						require.Same(t, oldPath, sm.Tunnel.DataPathPool[oldPath.PathID])
						require.True(t, oldPath.Activated)
						require.Same(t, oldPath, sm.Tunnel.DataPathPool.GetDefaultPath())
						require.Equal(t, oldCharging, oldPath.GetChargingUrr(sm))
						require.Same(t, oldInfo, sm.ChargingInfo[oldCharging[0].URRID])
						require.Same(t, oldData, sm.ChargingData["charging"])
						require.Same(t, oldTC, sm.TrafficControlDatas["route"])
					} else {
						require.NoError(t, err)
						current := sm.PCCRules["charged"].Datapath
						require.NotSame(t, oldPath, current)
						require.Same(t, oldPath, sm.DataPathToBeRemoved[oldPath.PathID])
						require.NotContains(t, sm.DataPathToBeRemoved, current.PathID)
						sm.PostRemoveDataPath()
						require.NotContains(t, sm.Tunnel.DataPathPool, oldPath.PathID)
						require.Same(t, current, sm.Tunnel.DataPathPool[current.PathID])
						require.True(t, current.Activated)
						require.Same(t, current, sm.Tunnel.DataPathPool.GetDefaultPath())
						require.Len(t, current.GetChargingUrr(sm), 1)
					}
					require.Len(t, sm.Tunnel.DataPathPool, 1)
				})
			}
		}
	}
}
