package context

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/util/idgenerator"
)

const testSessRuleID = "SessRuleId-1"

// newGSMTestContext builds a minimal deterministic SMContext for golden tests.
// It intentionally does NOT use NewSMContext: that would touch the global
// TeidGenerator (nil in unit tests) and the smContextPool.
func newGSMTestContext() *SMContext {
	sessRule := NewSessionRule(&models.Pcf_SMPolCtrl_SessionRule{
		AuthSessAmbr: &models.Ambr{Uplink: "200 Kbps", Downlink: "100 Kbps"},
		AuthDefQos:   &models.Pcf_SMPolCtrl_AuthorizedDefaultQos{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}},
		SessRuleId:   testSessRuleID,
	})
	return &SMContext{
		Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{
			Dnn:    "internet",
			SNssai: &models.Snssai{Sst: 1, Sd: "112232"},
			AnType: models.AccessType_3_GPP_ACCESS,
		},
		PDUSessionID:                 10,
		Pti:                          1,
		SelectedPDUSessionType:       nasie.PDUSessType_IPv4,
		PDUAddress:                   net.ParseIP("10.60.0.1").To4(),
		ProtocolConfigurationOptions: &ProtocolConfigurationOptions{},
		SessionRules:                 map[string]*SessionRule{testSessRuleID: sessRule},
		SelectedSessionRuleID:        testSessRuleID,
		defRuleID:                    1,
		QoSRuleIDGenerator:           idgenerator.NewGenerator(2, 255), // 1 is taken by defRuleID
		PacketFilterIDGenerator:      idgenerator.NewGenerator(1, 255),
		PCCRuleIDToQoSRuleID:         map[string]uint8{},
		PacketFilterIDToNASPFID:      map[string]uint8{},
	}
}

func TestGoldenBuildGSMPDUSessionEstablishmentAccept(t *testing.T) {
	testCases := []struct {
		name          string
		setupContext  func() *SMContext
		wantCause     uint8 // 0 = Cause5GSM IE absent
		wantQoSRules  int
		wantPFsInRule int // packet filters in the 2nd QoS rule; 0 = skip check
		golden        []byte
	}{
		{
			name:         "Minimal",
			setupContext: newGSMTestContext,
			wantCause:    0,
			wantQoSRules: 1,
			golden: []byte{
				0x2e, 0x0a, 0x01, 0xc2, 0x11, 0x00, 0x09, 0x01, 0x00, 0x06, 0x31, 0x31,
				0x01, 0x01, 0xff, 0x01, 0x06, 0x01, 0x00, 0x64, 0x01, 0x00, 0xc8, 0x29,
				0x05, 0x01, 0x0a, 0x3c, 0x00, 0x01, 0x22, 0x04, 0x01, 0x11, 0x22, 0x32,
				0x79, 0x00, 0x06, 0x01, 0x20, 0x41, 0x01, 0x01, 0x09, 0x25, 0x09, 0x08,
				0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x65, 0x74,
			},
		},
		{
			// Minimal plus the Always-on PDU session indication 0x80, "not
			// allowed" (TS 24.501 6.4.1.3 b) i)), after the S-NSSAI
			name: "AlwaysOnRequested",
			setupContext: func() *SMContext {
				smContext := newGSMTestContext()
				smContext.AlwaysOnPDUSessionRequested = true
				return smContext
			},
			wantCause:    0,
			wantQoSRules: 1,
			golden: []byte{
				0x2e, 0x0a, 0x01, 0xc2, 0x11, 0x00, 0x09, 0x01, 0x00, 0x06, 0x31, 0x31,
				0x01, 0x01, 0xff, 0x01, 0x06, 0x01, 0x00, 0x64, 0x01, 0x00, 0xc8, 0x29,
				0x05, 0x01, 0x0a, 0x3c, 0x00, 0x01, 0x22, 0x04, 0x01, 0x11, 0x22, 0x32,
				0x80, 0x79, 0x00, 0x06, 0x01, 0x20, 0x41, 0x01, 0x01, 0x09, 0x25, 0x09,
				0x08, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x65, 0x74,
			},
		},
		{
			name: "Full",
			setupContext: func() *SMContext {
				smContext := newGSMTestContext()
				smContext.EstAcceptCause5gSMValue = nasie.Cause5GSM_PDUSessTypeIpv4OnlyAllowed
				// exactly ONE PCC rule / ONE additional QoS flow: two or more
				// makes the golden unstable (map iteration + rule ID allocation)
				smContext.PCCRules = map[string]*PCCRule{
					"PccRuleId-1": {
						Pcf_SMPolCtrl_PccRule: &models.Pcf_SMPolCtrl_PccRule{
							PccRuleId:  "PccRuleId-1",
							Precedence: 200,
							FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{{
								FlowDescription: "permit out ip from any to assigned",
								PackFiltId:      "PackFiltId-1",
								FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
							}},
						},
						QFI: 2,
					},
				}
				smContext.AdditionalQosFlows = map[uint8]*QoSFlow{
					2: NewQoSFlow(2, &models.Pcf_SMPolCtrl_QosData{
						Var5qi: 9,
						Arp: &models.Arp{
							PriorityLevel: 8,
							PreemptCap:    models.PreemptionCapability_NOT_PREEMPT,
							PreemptVuln:   models.PreemptionVulnerability_NOT_PREEMPTABLE,
						},
					}),
				}
				smContext.ProtocolConfigurationOptions = &ProtocolConfigurationOptions{
					DNSIPv4Request:     true,
					IPv4LinkMTURequest: true,
				}
				smContext.DNNInfo = &SnssaiSmfDnnInfo{
					DNS: DNS{IPv4Addr: net.ParseIP("8.8.8.8").To4()},
				}
				return smContext
			},
			wantCause:    nasie.Cause5GSM_PDUSessTypeIpv4OnlyAllowed,
			wantQoSRules: 2,
			golden: []byte{
				0x2e, 0x0a, 0x01, 0xc2, 0x11, 0x00, 0x12, 0x01, 0x00, 0x06, 0x31, 0x31,
				0x01, 0x01, 0xff, 0x01, 0x02, 0x00, 0x06, 0x21, 0x31, 0x01, 0x01, 0xc8,
				0x02, 0x06, 0x01, 0x00, 0x64, 0x01, 0x00, 0xc8, 0x59, 0x32, 0x29, 0x05,
				0x01, 0x0a, 0x3c, 0x00, 0x01, 0x22, 0x04, 0x01, 0x11, 0x22, 0x32, 0x79,
				0x00, 0x0c, 0x01, 0x20, 0x41, 0x01, 0x01, 0x09, 0x02, 0x20, 0x41, 0x01,
				0x01, 0x09, 0x7b, 0x00, 0x0d, 0x80, 0x00, 0x0d, 0x04, 0x08, 0x08, 0x08,
				0x08, 0x00, 0x10, 0x02, 0x05, 0x78, 0x25, 0x09, 0x08, 0x69, 0x6e, 0x74,
				0x65, 0x72, 0x6e, 0x65, 0x74,
			},
		},
		{
			// Exercises the packet-filter forms Minimal/Full do not reach:
			// address CIDR (local+remote), single port, port range, protocol
			// number, ToS marking, IPsec SPI, and the src-only/dst-only port
			// cases of TS 24.501 9.11.4.13.4 — plus the GBR branch of
			// BuildNasQoSDesc (GFBR/MFBR parameters from bitrate strings).
			name: "GBRFlowAndPacketFilterForms",
			setupContext: func() *SMContext {
				smContext := newGSMTestContext()
				// exactly ONE PCC rule (map!): FlowInfos is a slice, so
				// multiple packet filters stay deterministic
				smContext.PCCRules = map[string]*PCCRule{
					"PccRuleId-2": {
						Pcf_SMPolCtrl_PccRule: &models.Pcf_SMPolCtrl_PccRule{
							PccRuleId:  "PccRuleId-2",
							Precedence: 100,
							FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{
								{
									// remote addr + single remote port, local addr +
									// local port range, protocol TCP(6)
									FlowDescription: "permit out 6 from 10.10.0.0/16 1000 to 10.60.0.1/32 2000-3000",
									PackFiltId:      "PackFiltId-2",
									FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
								},
								{
									// remote port range + single local port + ToS, UDP(17)
									FlowDescription: "permit out 17 from 10.20.0.0/16 5000-6000 to 10.60.0.1/32 443",
									TosTrafficClass: "28ff",
									PackFiltId:      "PackFiltId-3",
									FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK,
								},
								{
									// local-port-only case (srcPLen==0 && dstPLen>0)
									FlowDescription: "permit out ip from any to 10.60.0.1/32 8080",
									PackFiltId:      "PackFiltId-4",
									FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_UPLINK,
								},
								{
									// remote-port-only case (srcPLen>0 && dstPLen==0) + SPI
									FlowDescription: "permit out ip from 10.30.0.0/16 9000 to assigned",
									Spi:             "1a2b3c",
									PackFiltId:      "PackFiltId-5",
									FlowDirection:   models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
								},
							},
						},
						QFI: 2,
					},
				}
				smContext.AdditionalQosFlows = map[uint8]*QoSFlow{
					2: NewQoSFlow(2, &models.Pcf_SMPolCtrl_QosData{
						Var5qi:  1, // standard GBR 5QI -> IsGBRFlow() is true
						GbrDl:   "100 Mbps",
						GbrUl:   "50 Mbps",
						MaxbrDl: "200 Mbps",
						MaxbrUl: "150 Mbps",
						Arp: &models.Arp{
							PriorityLevel: 8,
							PreemptCap:    models.PreemptionCapability_NOT_PREEMPT,
							PreemptVuln:   models.PreemptionVulnerability_NOT_PREEMPTABLE,
						},
					}),
				}
				return smContext
			},
			wantCause:     0,
			wantQoSRules:  2,
			wantPFsInRule: 4, // FlowInfo 1-4 yield one filter each (1x1 cross product)
			// Golden updated for the new nas module (accepted wire-format
			// changes; same lengths, same values, only ordering differs):
			//  1. packet filter components are emitted in ie.PacketFilterContents'
			//     fixed field order (remote addr, local addr, ports, proto,
			//     ToS/SPI) instead of the old caller-append order;
			//  2. QoS flow description parameters are emitted in parameter-ID
			//     order (GFBR UL 02, GFBR DL 03, MFBR UL 04, MFBR DL 05) instead
			//     of the old DL-before-UL append order.
			// TS 24.501 parses both lists type-driven; order is not mandated.
			golden: []byte{
				0x2e, 0x0a, 0x01, 0xc2, 0x11, 0x00, 0x6f, 0x01, 0x00, 0x06, 0x31, 0x31,
				0x01, 0x01, 0xff, 0x01, 0x02, 0x00, 0x63, 0x24, 0x31, 0x1c, 0x10, 0x0a,
				0x0a, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00, 0x11, 0x0a, 0x3c, 0x00, 0x01,
				0xff, 0xff, 0xff, 0xff, 0x50, 0x03, 0xe8, 0x41, 0x07, 0xd0, 0x0b, 0xb8,
				0x30, 0x06, 0x12, 0x1f, 0x10, 0x0a, 0x14, 0x00, 0x00, 0xff, 0xff, 0x00,
				0x00, 0x11, 0x0a, 0x3c, 0x00, 0x01, 0xff, 0xff, 0xff, 0xff, 0x51, 0x13,
				0x88, 0x17, 0x70, 0x40, 0x01, 0xbb, 0x30, 0x11, 0x70, 0x28, 0xff, 0x23,
				0x0c, 0x11, 0x0a, 0x3c, 0x00, 0x01, 0xff, 0xff, 0xff, 0xff, 0x40, 0x1f,
				0x90, 0x34, 0x11, 0x10, 0x0a, 0x1e, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00,
				0x50, 0x23, 0x28, 0x60, 0x00, 0x1a, 0x2b, 0x3c, 0x64, 0x02, 0x06, 0x01,
				0x00, 0x64, 0x01, 0x00, 0xc8, 0x29, 0x05, 0x01, 0x0a, 0x3c, 0x00, 0x01,
				0x22, 0x04, 0x01, 0x11, 0x22, 0x32, 0x79, 0x00, 0x20, 0x01, 0x20, 0x41,
				0x01, 0x01, 0x09, 0x02, 0x20, 0x45, 0x01, 0x01, 0x01, 0x02, 0x03, 0x06,
				0x00, 0x32, 0x03, 0x03, 0x06, 0x00, 0x64, 0x04, 0x03, 0x06, 0x00, 0x96,
				0x05, 0x03, 0x06, 0x00, 0xc8, 0x25, 0x09, 0x08, 0x69, 0x6e, 0x74, 0x65,
				0x72, 0x6e, 0x65, 0x74,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			smContext := tc.setupContext()

			got, err := BuildGSMPDUSessionEstablishmentAccept(smContext)
			require.NoError(t, err)

			// double-encode guard needs a fresh context: the builder allocates
			// QoS rule IDs from QoSRuleIDGenerator on every call
			got2, err := BuildGSMPDUSessionEstablishmentAccept(tc.setupContext())
			require.NoError(t, err)
			require.Equal(t, got, got2)

			require.Equal(t, tc.golden, got)

			gsmMsg, errParse := message.ParseGSM(tc.golden)
			require.NoError(t, errParse)
			accept, ok := gsmMsg.(*message.PDUSessEstAccept)
			require.True(t, ok)
			require.Equal(t, message.MsgTypePDUSessEstAccept, accept.MsgType())
			require.Equal(t, uint8(1), accept.ProcedureTransactionID())
			require.Equal(t, uint8(10), accept.PDUSessionID())

			require.NotNil(t, accept.PDUAddr)
			require.Equal(t, []byte(net.ParseIP("10.60.0.1").To4()), accept.PDUAddr.IPv4)

			require.NotNil(t, accept.AuthoQosRules)
			require.Len(t, accept.AuthoQosRules.Rules, tc.wantQoSRules)
			if tc.wantPFsInRule > 0 {
				require.Len(t, accept.AuthoQosRules.Rules[1].PktFilterList, tc.wantPFsInRule)
			}

			if tc.wantCause == 0 {
				require.Nil(t, accept.Cause5GSM)
			} else {
				require.NotNil(t, accept.Cause5GSM)
				require.Equal(t, tc.wantCause, accept.Cause5GSM.Value)
			}
		})
	}
}

func TestGoldenBuildGSMPDUSessionEstablishmentReject(t *testing.T) {
	smContext := newGSMTestContext()

	golden := []byte{0x2e, 0x0a, 0x01, 0xc3, 0x26}

	got, err := BuildGSMPDUSessionEstablishmentReject(smContext, nasie.Cause5GSM_NwFailure)
	require.NoError(t, err)

	// double-encode guard: same fixture must yield identical bytes
	got2, err := BuildGSMPDUSessionEstablishmentReject(smContext, nasie.Cause5GSM_NwFailure)
	require.NoError(t, err)
	require.Equal(t, got, got2)

	require.Equal(t, golden, got)

	// decode-back check
	gsmMsg, errParse := message.ParseGSM(golden)
	require.NoError(t, errParse)
	reject, ok := gsmMsg.(*message.PDUSessEstRej)
	require.True(t, ok)
	require.Equal(t, message.MsgTypePDUSessEstRej, reject.MsgType())
	require.Equal(t, uint8(1), reject.ProcedureTransactionID())
	require.Equal(t, uint8(10), reject.PDUSessionID())
	require.NotNil(t, reject.Cause5GSM)
	require.Equal(t, nasie.Cause5GSM_NwFailure, reject.Cause5GSM.Value)
}

func TestGoldenBuildGSMPDUSessionReleaseCommand(t *testing.T) {
	testCases := []struct {
		name            string
		isTriggeredByUE bool
		wantPTI         uint8
		golden          []byte
	}{
		{
			name:            "TriggeredByUE",
			isTriggeredByUE: true,
			wantPTI:         1,
			golden:          []byte{0x2e, 0x0a, 0x01, 0xd3, 0x24},
		},
		{
			name:            "TriggeredByNetwork",
			isTriggeredByUE: false,
			wantPTI:         0,
			golden:          []byte{0x2e, 0x0a, 0x00, 0xd3, 0x24},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			smContext := newGSMTestContext()

			got, err := BuildGSMPDUSessionReleaseCommand(
				smContext, nasie.Cause5GSM_RegularDeactivation, tc.isTriggeredByUE)
			require.NoError(t, err)

			got2, err := BuildGSMPDUSessionReleaseCommand(
				smContext, nasie.Cause5GSM_RegularDeactivation, tc.isTriggeredByUE)
			require.NoError(t, err)
			require.Equal(t, got, got2)

			require.Equal(t, tc.golden, got)

			gsmMsg, errParse := message.ParseGSM(tc.golden)
			require.NoError(t, errParse)
			command, ok := gsmMsg.(*message.PDUSessRelCmd)
			require.True(t, ok)
			require.Equal(t, message.MsgTypePDUSessRelCmd, command.MsgType())
			require.Equal(t, tc.wantPTI, command.ProcedureTransactionID())
			require.Equal(t, uint8(10), command.PDUSessionID())
			require.NotNil(t, command.Cause5GSM)
			require.Equal(t, nasie.Cause5GSM_RegularDeactivation, command.Cause5GSM.Value)
		})
	}
}

func TestGoldenBuildGSMPDUSessionModificationCommand(t *testing.T) {
	smContext := newGSMTestContext()

	golden := []byte{0x2e, 0x0a, 0x01, 0xcb}

	got, err := BuildGSMPDUSessionModificationCommand(smContext)
	require.NoError(t, err)

	got2, err := BuildGSMPDUSessionModificationCommand(smContext)
	require.NoError(t, err)
	require.Equal(t, got, got2)

	require.Equal(t, golden, got)

	gsmMsg, errParse := message.ParseGSM(golden)
	require.NoError(t, errParse)
	command, ok := gsmMsg.(*message.PDUSessModCmd)
	require.True(t, ok)
	require.Equal(t, message.MsgTypePDUSessModCmd, command.MsgType())
	require.Equal(t, uint8(1), command.ProcedureTransactionID())
	require.Equal(t, uint8(10), command.PDUSessionID())
}

func TestGoldenBuildGSMPDUSessionReleaseReject(t *testing.T) {
	smContext := newGSMTestContext()

	golden := []byte{0x2e, 0x0a, 0x01, 0xd2, 0x1f}

	got, err := BuildGSMPDUSessionReleaseReject(smContext)
	require.NoError(t, err)

	got2, err := BuildGSMPDUSessionReleaseReject(smContext)
	require.NoError(t, err)
	require.Equal(t, got, got2)

	require.Equal(t, golden, got)

	gsmMsg, errParse := message.ParseGSM(golden)
	require.NoError(t, errParse)
	reject, ok := gsmMsg.(*message.PDUSessRelRej)
	require.True(t, ok)
	require.Equal(t, message.MsgTypePDUSessRelRej, reject.MsgType())
	require.Equal(t, uint8(1), reject.ProcedureTransactionID())
	require.Equal(t, uint8(10), reject.PDUSessionID())
	require.NotNil(t, reject.Cause5GSM)
	require.Equal(t, nasie.Cause5GSM_ReqRejected, reject.Cause5GSM.Value)
}

func TestGoldenBuildGSMPDUSessionModificationReject(t *testing.T) {
	smContext := newGSMTestContext()

	golden := []byte{0x2e, 0x0a, 0x01, 0xca, 0x61}

	got, err := BuildGSMPDUSessionModificationReject(smContext)
	require.NoError(t, err)

	got2, err := BuildGSMPDUSessionModificationReject(smContext)
	require.NoError(t, err)
	require.Equal(t, got, got2)

	require.Equal(t, golden, got)

	gsmMsg, errParse := message.ParseGSM(golden)
	require.NoError(t, errParse)
	reject, ok := gsmMsg.(*message.PDUSessModRej)
	require.True(t, ok)
	require.Equal(t, message.MsgTypePDUSessModRej, reject.MsgType())
	require.Equal(t, uint8(1), reject.ProcedureTransactionID())
	require.Equal(t, uint8(10), reject.PDUSessionID())
	require.NotNil(t, reject.Cause5GSM)
	require.Equal(t, nasie.Cause5GSM_MsgTypeNonExistentOrNotImpl, reject.Cause5GSM.Value)
}
