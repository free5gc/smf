package processor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/message"

	nasMessage "github.com/free5gc/nas/message"
	"github.com/free5gc/ngap/aper"
	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

const releasedTestCPNodeID = "127.0.0.1"

type releasedTestEnv struct {
	p    *Processor
	sm   *smf_context.SMContext
	pfcp *tiPFCPClient
}

// newReleasedTestEnv builds an established PDU session (default data path on
// one associated UPF, N4 session up); PFCP Session Modification Requests are
// captured and accepted.
func newReleasedTestEnv(t *testing.T) *releasedTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Description: "released SM context regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{},
			PFCP: &factory.PFCP{ExternalAddr: releasedTestCPNodeID, NodeID: releasedTestCPNodeID},
		},
	}))
	oldConfig := factory.SmfConfig
	factory.SmfConfig = &factory.Config{Configuration: &factory.Configuration{
		T3591: &factory.TimerValue{Enable: false}, T3592: &factory.TimerValue{Enable: false},
	}}
	t.Cleanup(func() { factory.SmfConfig = oldConfig })

	upf := smf_context.NewUPF(&pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("192.0.2.62").To4(),
	}, nil)
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)

	sm := smf_context.NewSMContext("imsi-208930000000262", 5)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000262", PduSessionId: 5,
	}
	sm.PFCPContext["192.0.2.62"] = &smf_context.PFCPSessionContext{LocalSEID: 1, RemoteSEID: 2}
	sm.LocalULTeid = 0x101
	newPDR := func(id uint16) *smf_context.PDR {
		return &smf_context.PDR{
			PDRID: id, Precedence: 255, State: smf_context.RULE_CREATE,
			FAR: &smf_context.FAR{
				FARID: uint32(id), State: smf_context.RULE_CREATE,
				ForwardingParameters: &smf_context.ForwardingParameters{},
			},
		}
	}
	node := smf_context.NewDataPathNode()
	node.UPF = upf
	node.UpLinkTunnel.PDR, node.DownLinkTunnel.PDR = newPDR(1), newPDR(2)
	dataPath := smf_context.NewDataPath()
	dataPath.FirstDPNode, dataPath.IsDefaultPath, dataPath.Activated = node, true, true
	sm.Tunnel.DataPathPool[1] = dataPath
	sm.UpCnxState = models.Smf_PDUSess_UpCnxState_ACTIVATED
	sm.SetState(smf_context.Active)

	client := &tiPFCPClient{requests: make(chan *message.SessionModificationRequest, 4)}
	p := newEventExposureProcessor(t)
	p.SetActivePFCPClient(client)
	return &releasedTestEnv{p: p, sm: sm, pfcp: client}
}

// release puts the session where a UE-requested release leaves it: N4 session
// deleted, no AN tunnel known, state InActivePending.
func (e *releasedTestEnv) release() {
	e.sm.PFCPReleaseDone = true
	e.sm.PFCPContext["192.0.2.62"].RemoteSEID = 0
	e.sm.Tunnel.ANInformation.IPAddress, e.sm.Tunnel.ANInformation.TEID = nil, 0
	e.sm.SetState(smf_context.InActivePending)
}

func (e *releasedTestEnv) update(t *testing.T, body models.UpdateSmContextRequestBody) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	e.p.HandlePDUSessionSMContextUpdate(c, body, e.sm.Ref)
	return rec
}

func releasedTestSetupResponse(t *testing.T) models.UpdateSmContextRequestBody {
	t.Helper()
	transfer, err := ngapie.MarshalBinary(&ngapie.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: &ngapie.QosFlowPerTNLInformation{
			UPTransportLayerInformation: &ngapie.UPTransportLayerInformation{Choice: &ngapie.GTPTunnel{
				TransportLayerAddress: &ngapie.TransportLayerAddress{
					Value: aper.BitString{Bytes: net.ParseIP("10.100.0.5").To4(), BitLength: 32},
				},
				GTPTEID: &ngapie.GTPTEID{Value: []byte{0, 0, 0, 0x42}},
			}},
			AssociatedQosFlowList: &ngapie.AssociatedQosFlowList{List: []ngapie.AssociatedQosFlowItem{
				{QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: 1}},
			}},
		},
	})
	require.NoError(t, err)
	return models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PDU_RES_SETUP_RSP,
			N2SmInfo:     &models.RefToBinaryData{ContentId: "ReleasedN2SmInfo"},
		},
		BinaryDataN2SmInformation: &multipart.RelatedContent{ContentID: "ReleasedN2SmInfo", Content: transfer},
	}
}

func releasedTestUpCnxState(state models.Smf_PDUSess_UpCnxState) models.UpdateSmContextRequestBody {
	return models.UpdateSmContextRequestBody{JsonData: &models.Smf_PDUSess_SmContextUpdateData{UpCnxState: state}}
}

func releasedTestReleaseComplete(t *testing.T) models.UpdateSmContextRequestBody {
	t.Helper()
	n1, err := (&nasMessage.PDUSessRelComplete{PDUSessId: 5, PTI: 2}).MarshalBinary()
	require.NoError(t, err)
	return models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N1SmMsg: &models.RefToBinaryData{ContentId: "ReleasedN1SmMsg"},
		},
		BinaryDataN1SmMessage: &multipart.RelatedContent{ContentID: "ReleasedN1SmMsg", Content: n1},
	}
}

func releasedTestRemoved(ref string) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if smf_context.GetSMContextByRef(ref) == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// A PDU Session Resource Setup Response that arrives after the UE released the
// session must not revive it: no N4 modification, no Active state, and the
// following Release Complete removes the SM context.
func TestLateSetupResponseAfterReleaseKeepsSessionReleased(t *testing.T) {
	e := newReleasedTestEnv(t)
	e.sm.UpCnxState = models.Smf_PDUSess_UpCnxState_ACTIVATING
	e.release()

	rec := e.update(t, releasedTestSetupResponse(t))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Equal(t, smf_context.InActivePending, e.sm.State())
	require.Nil(t, e.sm.Tunnel.ANInformation.IPAddress, "late Setup Response applied")
	require.Empty(t, e.pfcp.requests)

	rec = e.update(t, releasedTestReleaseComplete(t))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, releasedTestRemoved(e.sm.Ref), "Release Complete did not remove the SM context")
}

// A DEACTIVATED update (AN release) on a released session, before or after
// the Release Complete, is answered DEACTIVATED without any N4 modification
// and leaves the session released.
func TestDeactivateOnReleasedSessionKeepsSessionReleased(t *testing.T) {
	for _, state := range []smf_context.SMContextState{smf_context.InActivePending, smf_context.InActive} {
		t.Run(state.String(), func(t *testing.T) {
			e := newReleasedTestEnv(t)
			e.release()
			e.sm.SetState(state)

			rec := e.update(t, releasedTestUpCnxState(models.Smf_PDUSess_UpCnxState_DEACTIVATED))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"upCnxState":"DEACTIVATED"`)
			require.Equal(t, state, e.sm.State())
			require.Empty(t, e.pfcp.requests)
		})
	}
}

// A request to activate (service request) or move (path switch) the user plane
// of a released session is refused with upCnxState DEACTIVATED and no N2 Setup
// Request (TS 24.501 6.3.3.5 d, TS 29.502 5.2.2.3.2.2 step 2b).
func TestActivationOfReleasedSessionIsRefused(t *testing.T) {
	for name, body := range map[string]models.UpdateSmContextRequestBody{
		"ACTIVATING": releasedTestUpCnxState(models.Smf_PDUSess_UpCnxState_ACTIVATING),
		"PATH_SWITCH_REQ": {JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PATH_SWITCH_REQ,
		}},
		"HO PREPARING": {JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			HoState: models.Smf_PDUSess_HoState_PREPARING,
		}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newReleasedTestEnv(t)
			e.release()

			rec := e.update(t, body)
			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"upCnxState":"DEACTIVATED"`)
			require.NotContains(t, rec.Body.String(), "PDU_RES_SETUP_REQ")
			require.Equal(t, smf_context.InActivePending, e.sm.State())
			require.Empty(t, e.pfcp.requests)
		})
	}
}

// Control: an established session left in ModificationPending by a PCF policy
// update (HandleSMPolicyUpdateNotify never sets it back to Active) still
// applies a Setup Response and a deactivation.
func TestUpdatesAfterPolicyUpdateStillApply(t *testing.T) {
	t.Run("PDU_RES_SETUP_RSP", func(t *testing.T) {
		e := newReleasedTestEnv(t)
		e.sm.SetState(smf_context.ModificationPending)
		rec := e.update(t, releasedTestSetupResponse(t))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, net.ParseIP("10.100.0.5").To4(), e.sm.Tunnel.ANInformation.IPAddress)
		require.Len(t, e.pfcp.requests, 1)
		require.Equal(t, smf_context.Active, e.sm.State())
	})
	t.Run("DEACTIVATED", func(t *testing.T) {
		e := newReleasedTestEnv(t)
		e.sm.SetState(smf_context.ModificationPending)
		rec := e.update(t, releasedTestUpCnxState(models.Smf_PDUSess_UpCnxState_DEACTIVATED))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, models.Smf_PDUSess_UpCnxState_DEACTIVATED, e.sm.UpCnxState)
		require.Len(t, e.pfcp.requests, 1)
		require.Equal(t, smf_context.Active, e.sm.State())
	})
}
