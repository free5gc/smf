package context_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	goPfcpMessage "github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
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

const (
	updatedFlowMBR     = "20 Mbps"
	qosTestPFCPAddress = "127.0.0.1"
)

// Marshal and parse the actual pending rules on each UPF, as activation does.
func qosUpdateRequests(t *testing.T, sm *smf_context.SMContext) map[string]*goPfcpMessage.SessionModificationRequest {
	t.Helper()
	requests := make(map[string]*goPfcpMessage.SessionModificationRequest)
	for ip, session := range sm.PFCPContext {
		session.RemoteSEID = 123
		var pdrs []*smf_context.PDR
		var qers []*smf_context.QER
		fars := make(map[uint32]*smf_context.FAR)
		for _, pdr := range session.PDRs {
			pdrs = append(pdrs, pdr)
			qers = append(qers, pdr.QER...)
			fars[pdr.FAR.FARID] = pdr.FAR
		}
		var farList []*smf_context.FAR
		for _, far := range fars {
			farList = append(farList, far)
		}
		request, err := pfcp_message.BuildSessionModificationRequest(ip, session.NodeID.ResolveNodeIdToIp().String(),
			sm, pdrs, farList, nil, qers, nil)
		require.NoError(t, err)
		wire, err := request.Marshal()
		require.NoError(t, err)
		parsed, err := goPfcpMessage.ParseSessionModificationRequest(wire)
		require.NoError(t, err)
		requests[ip] = parsed
	}
	return requests
}

func markingQER(t *testing.T, pdr *smf_context.PDR, qfi uint8) *smf_context.QER {
	t.Helper()
	for _, qer := range pdr.QER {
		if qer.QFI != nil && qer.QFI.QFI == qfi {
			return qer
		}
	}
	t.Fatalf("PDR %d has no QER for QFI %d", pdr.PDRID, qfi)
	return nil
}

func TestSameQoSIDUpdatesFlowQERProfileOnWire(t *testing.T) {
	mbr := models.Pcf_SMPolCtrl_QosData{Var5qi: 9, MaxbrUl: "10 Mbps", MaxbrDl: "10 Mbps"}
	gbr := models.Pcf_SMPolCtrl_QosData{
		Var5qi: 1, MaxbrUl: "10 Mbps", MaxbrDl: "10 Mbps", GbrUl: "5 Mbps", GbrDl: "5 Mbps",
	}
	increased, decreased, equivalent, changedGBR := mbr, mbr, mbr, gbr
	increased.MaxbrUl, increased.MaxbrDl = updatedFlowMBR, updatedFlowMBR
	decreased.MaxbrUl, decreased.MaxbrDl = "2 Mbps", "2 Mbps"
	equivalent.MaxbrUl, equivalent.MaxbrDl = "10000 Kbps", "10000 Kbps"
	changedGBR.GbrUl, changedGBR.GbrDl = "8 Mbps", "8 Mbps"
	for _, tc := range []struct {
		name        string
		before      models.Pcf_SMPolCtrl_QosData
		after       models.Pcf_SMPolCtrl_QosData
		mbr, gbr    uint64
		replacement bool
	}{
		{"increase MBR", mbr, increased, 20000, 0, true},
		{"decrease MBR", mbr, decreased, 2000, 0, true},
		{"remove MBR", mbr, models.Pcf_SMPolCtrl_QosData{Var5qi: 9}, 0, 0, true},
		{"add MBR", models.Pcf_SMPolCtrl_QosData{Var5qi: 9}, mbr, 10000, 0, true},
		{"change GBR", gbr, changedGBR, 10000, 8000, true},
		{"add GBR", mbr, gbr, 10000, 5000, true},
		{"remove GBR", gbr, mbr, 10000, 0, true},
		{"remove GBR and MBR", gbr, models.Pcf_SMPolCtrl_QosData{Var5qi: 9}, 0, 0, true},
		{"equivalent rate units", mbr, equivalent, 10000, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := newTIPolicyContext(t)
			self := smf_context.GetSelf()
			originalAddress := self.ExternalAddr
			self.ExternalAddr = qosTestPFCPAddress
			t.Cleanup(func() { self.ExternalAddr = originalAddress })
			installDefault(t, sm)
			tc.before.QosId, tc.after.QosId = "same-qos", "same-qos"
			require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
				PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{
					"service": tiPCC("service", "same-qos", "", 100),
					"shared":  tiPCC("shared", "same-qos", "", 80),
				},
				QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{"same-qos": &tc.before},
			}))
			static := activateStaticMEC(t, sm)
			qfi := sm.PCCRules["service"].QFI
			old := make(map[string]*smf_context.QER)
			for node := sm.PCCRules["service"].Datapath.FirstDPNode; node != nil; node = node.Next() {
				old[node.GetNodeIP()] = markingQER(t, node.UpLinkTunnel.PDR, qfi)
			}
			qosUpdateRequests(t, sm) // The old rules are now installed.
			require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
				QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{"same-qos": &tc.after},
			}))
			for _, ruleID := range []string{"service", "shared"} {
				require.Equal(t, qfi, sm.PCCRules[ruleID].QFI)
			}
			current := make(map[string]*smf_context.QER)
			for node := sm.PCCRules["service"].Datapath.FirstDPNode; node != nil; node = node.Next() {
				qer := markingQER(t, node.UpLinkTunnel.PDR, qfi)
				current[node.GetNodeIP()] = qer
				require.Equal(t, tc.replacement, old[node.GetNodeIP()].QERID != qer.QERID)
				// Old settings remain intact until old PDRs leave the UPF.
				require.Same(t, old[node.GetNodeIP()], node.UPF.GetQERById(old[node.GetNodeIP()].QERID))
			}
			for _, path := range []*smf_context.DataPath{sm.PCCRules["shared"].Datapath, static} {
				for node := path.FirstDPNode; node != nil; node = node.Next() {
					for _, tunnel := range []*smf_context.GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
						for _, pdr := range append([]*smf_context.PDR{tunnel.PDR}, tunnel.AdditionalPDRs...) {
							for _, qer := range pdr.QER {
								if qer.QFI != nil && qer.QFI.QFI == qfi {
									require.Same(t, current[node.GetNodeIP()], qer)
								}
							}
						}
					}
				}
			}
			for ip, request := range qosUpdateRequests(t, sm) {
				qer := current[ip]
				if !tc.replacement {
					require.Empty(t, request.CreateQER)
					require.Empty(t, request.RemoveQER)
					continue
				}
				require.Len(t, request.RemoveQER, 1)
				removed, err := request.RemoveQER[0].QERID()
				require.NoError(t, err)
				require.Equal(t, old[ip].QERID, removed)
				require.Len(t, request.CreateQER, 1)
				qerChildren, err := request.CreateQER[0].CreateQER()
				require.NoError(t, err)
				seenMBR, seenGBR, seenQFI, seenGate := false, false, false, false
				for _, child := range qerChildren {
					switch child.Type {
					case ie.QERID:
						id, decodeErr := child.QERID()
						require.NoError(t, decodeErr)
						require.Equal(t, qer.QERID, id)
					case ie.QFI:
						value, decodeErr := child.QFI()
						require.NoError(t, decodeErr)
						require.Equal(t, qfi, value)
						seenQFI = true
					case ie.GateStatus:
						value, decodeErr := child.GateStatus()
						require.NoError(t, decodeErr)
						require.Zero(t, value, "both gates must be open")
						seenGate = true
					case ie.MBR:
						value, decodeErr := child.MBR()
						require.NoError(t, decodeErr)
						require.Equal(t, ie.NewMBR(tc.mbr, tc.mbr).Payload, value)
						seenMBR = true
					case ie.GBR:
						value, decodeErr := child.GBR()
						require.NoError(t, decodeErr)
						require.Equal(t, ie.NewGBR(tc.gbr, tc.gbr).Payload, value)
						seenGBR = true
					}
				}
				require.True(t, seenQFI)
				require.True(t, seenGate)
				require.Equal(t, tc.mbr != 0, seenMBR)
				require.Equal(t, tc.gbr != 0, seenGBR)
				newReferences := 0
				for _, group := range append(request.CreatePDR, request.UpdatePDR...) {
					var children []*ie.IE
					if group.Type == ie.CreatePDR {
						children, err = group.CreatePDR()
					} else {
						children, err = group.UpdatePDR()
					}
					require.NoError(t, err)
					for _, child := range children {
						if child.Type == ie.QERID {
							id, decodeErr := child.QERID()
							require.NoError(t, decodeErr)
							require.NotEqual(t, old[ip].QERID, id, "new PDRs must not retain the stale QER")
							if id == qer.QERID {
								newReferences++
							}
						}
					}
				}
				require.Positive(t, newReferences, "the new QER must be referenced on the wire")
			}
			sm.PostRemoveDataPath()
			for node := sm.PCCRules["service"].Datapath.FirstDPNode; node != nil; node = node.Next() {
				if tc.replacement {
					require.Nil(t, node.UPF.GetQERById(old[node.GetNodeIP()].QERID))
				}
				require.Same(t, current[node.GetNodeIP()], node.UPF.GetQERById(current[node.GetNodeIP()].QERID))
			}
		})
	}
}

func TestNormalAndDCActivationReplaceChangedFlowQER(t *testing.T) {
	for _, activation := range []struct {
		name string
		run  func(*smf_context.DataPath, *smf_context.SMContext, uint32, ...smf_context.PathQoSBinding) error
	}{
		{"normal", (*smf_context.DataPath).ActivateTunnelAndPDR},
		{"DC", (*smf_context.DataPath).ActivateDcTunnelAndPDR},
	} {
		t.Run(activation.name, func(t *testing.T) {
			sm := newTIPolicyContext(t)
			installDefault(t, sm)
			activate := func(qos *models.Pcf_SMPolCtrl_QosData) (*smf_context.DataPath, error) {
				path := &smf_context.DataPath{FirstDPNode: sm.Tunnel.DataPathPool.GetDefaultPath().CopyFirstDPNode()}
				err := activation.run(path, sm, 100, smf_context.PathQoSBinding{QFI: 4, QosData: qos})
				return path, err
			}
			qos := &models.Pcf_SMPolCtrl_QosData{Var5qi: 9, MaxbrUl: "10 Mbps", MaxbrDl: "10 Mbps"}
			first, err := activate(qos)
			require.NoError(t, err)
			before := markingQER(t, first.FirstDPNode.UpLinkTunnel.PDR, 4)
			qos.MaxbrDl = "invalid"
			_, err = activate(qos)
			require.ErrorContains(t, err, "MBR downlink")
			require.Equal(t, uint64(10000), before.MBR.DLMBR, "invalid input must not mutate the installed QER")
			qos.MaxbrDl = "10 Mbps"
			unchanged, err := activate(qos)
			require.NoError(t, err)
			require.Same(t, before, markingQER(t, unchanged.FirstDPNode.UpLinkTunnel.PDR, 4))
			qos.MaxbrDl = updatedFlowMBR
			changed, err := activate(qos)
			require.NoError(t, err)
			after := markingQER(t, changed.FirstDPNode.UpLinkTunnel.PDR, 4)
			require.NotEqual(t, before.QERID, after.QERID)
			require.Equal(t, uint64(20000), after.MBR.DLMBR)
			require.Equal(t, uint64(10000), before.MBR.DLMBR)
			// Keep the old QER while old paths still use it.
			require.Same(t, before, first.FirstDPNode.UPF.GetQERById(before.QERID))
		})
	}
}

func TestSessionChargingURRSharedWithServicePDRs(t *testing.T) {
	for _, online := range []bool{false, true} {
		name := "offline"
		if online {
			name = "online"
		}
		t.Run(name, func(t *testing.T) {
			sm := newTIPolicyContext(t)
			self := smf_context.GetSelf()
			originalAddress := self.ExternalAddr
			self.ExternalAddr = qosTestPFCPAddress
			t.Cleanup(func() { self.ExternalAddr = originalAddress })
			defaultRule := tiPCC("default", "", "", 255)
			defaultRule.FlowInfos[0].FlowDescription = "permit out ip from any to assigned"
			defaultRule.RefChgData = []string{"session-charge"}
			service := tiPCC("service", "qos", "", 128)
			service.RefChgData = []string{"service-charge"}
			require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
				PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"default": defaultRule, "service": service},
				QosDecs: map[string]*models.Pcf_SMPolCtrl_QosData{
					"qos": {QosId: "qos", Var5qi: 8, MaxbrUl: "20 Mbps", MaxbrDl: "20 Mbps"},
				},
				ChgDecs: map[string]*models.Pcf_SMPolCtrl_ChargingData{
					"session-charge": {ChgId: "session-charge", RatingGroup: 1, Online: online, Offline: !online},
					"service-charge": {ChgId: "service-charge", RatingGroup: 2, Online: online, Offline: !online},
				},
			}))
			var anchor *smf_context.DataPathNode
			for node := sm.PCCRules["service"].Datapath.FirstDPNode; node != nil; node = node.Next() {
				if node.IsAnchorUPF() {
					anchor = node
				}
			}
			require.NotNil(t, anchor)
			var sessionURR *smf_context.URR
			for id, info := range sm.ChargingInfo {
				if info.UpfId == anchor.UPF.UUID() && info.RatingGroup == 1 {
					sessionURR = sm.GetUrrRule(info.UpfId, id)
				}
			}
			require.NotNil(t, sessionURR)
			for _, pdr := range []*smf_context.PDR{anchor.UpLinkTunnel.PDR, anchor.DownLinkTunnel.PDR} {
				require.Contains(t, pdr.URR, sessionURR)
			}
			require.Equal(t, 4, sm.UrrTable[anchor.UPF.UUID()][sessionURR.URRID].RefCount)

			request := qosUpdateRequests(t, sm)[anchor.GetNodeIP()]
			require.NotNil(t, request)
			for _, pdr := range []*smf_context.PDR{anchor.UpLinkTunnel.PDR, anchor.DownLinkTunnel.PDR} {
				found := false
				for _, group := range request.CreatePDR {
					children, err := group.CreatePDR()
					require.NoError(t, err)
					var id uint16
					var urrIDs []uint32
					for _, child := range children {
						switch child.Type {
						case ie.PDRID:
							id, err = child.PDRID()
						case ie.URRID:
							var urrID uint32
							urrID, err = child.URRID()
							urrIDs = append(urrIDs, urrID)
						}
						require.NoError(t, err)
					}
					if id == pdr.PDRID {
						require.Contains(t, urrIDs, sessionURR.URRID, "service PDR must reference session usage on the wire")
						found = true
					}
				}
				require.True(t, found)
			}
			// Removing the service must preserve the default path's shared URR.
			require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
				PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"service": nil},
			}))
			sm.PostRemoveDataPath()
			require.Equal(t, 2, sm.UrrTable[anchor.UPF.UUID()][sessionURR.URRID].RefCount)
			require.NotEqual(t, smf_context.RULE_REMOVE, sm.GetUrrState(anchor.UPF.UUID(), sessionURR.URRID))

			// Final charging metadata remains after deletion; it must not hide
			// a new live URR when the old session URR has left the UPF.
			require.NoError(t, sm.ApplyPccRules(&models.Pcf_SMPolCtrl_SmPolicyDecision{
				PccRules: map[string]*models.Pcf_SMPolCtrl_PccRule{"default": nil},
			}))
			sm.PostRemoveDataPath()
			require.Nil(t, sm.GetUrrRule(anchor.UPF.UUID(), sessionURR.URRID))
			replacement := (&smf_context.DataPath{}).GetOrCreateUrr(sm, anchor.UPF,
				&models.Pcf_SMPolCtrl_ChargingData{RatingGroup: 1, Online: online, Offline: !online},
				smf_context.PduSessionCharging)
			require.NotNil(t, replacement)
			require.NotEqual(t, sessionURR.URRID, replacement.URRID)
		})
	}
}
