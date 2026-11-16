package processor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	pfcpMessage "github.com/wmnsk/go-pfcp/message"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/nas/message"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

const relCompleteTestCPNodeID = "127.0.0.1"

type relCompleteTestEnv struct {
	p         *Processor
	sm        *smf_context.SMContext
	deletions atomic.Int32
}

// newRelCompleteTestEnv builds an established PDU session on one associated
// UPF and counts PFCP Session Deletion Requests.
func newRelCompleteTestEnv(t *testing.T) *relCompleteTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, smf_context.InitSmfContext(&factory.Config{
		Info: &factory.Info{Description: "Release Complete regression"},
		Configuration: &factory.Configuration{
			Sbi:  &factory.Sbi{},
			PFCP: &factory.PFCP{ExternalAddr: relCompleteTestCPNodeID, NodeID: relCompleteTestCPNodeID},
		},
	}))
	upf := smf_context.NewUPF(&pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.ParseIP("192.0.2.60").To4(),
	}, nil)
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)

	sm := smf_context.NewSMContext("imsi-208930000000260", 5)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000000260", PduSessionId: 5,
	}
	sm.PFCPContext["192.0.2.60"] = &smf_context.PFCPSessionContext{LocalSEID: 1, RemoteSEID: 2}
	node := smf_context.NewDataPathNode()
	node.UPF = upf
	dataPath := smf_context.NewDataPath()
	dataPath.FirstDPNode, dataPath.IsDefaultPath, dataPath.Activated = node, true, true
	sm.Tunnel.DataPathPool[1] = dataPath
	sm.SetState(smf_context.Active)

	e := &relCompleteTestEnv{p: newEventExposureProcessor(t), sm: sm}
	e.p.SetActivePFCPClient(&fakeActivePFCPClient{deleteCtx: func(
		_ context.Context, request *pfcpMessage.SessionDeletionRequest, _ *net.UDPAddr, localSEID uint64,
	) (*pfcpMessage.SessionDeletionResponse, error) {
		e.deletions.Add(1)
		return pfcpMessage.NewSessionDeletionResponse(0, 0, localSEID, request.Sequence(), 0,
			ie.NewCause(ie.CauseRequestAccepted)), nil
	}})
	return e
}

func (e *relCompleteTestEnv) update(t *testing.T, body models.UpdateSmContextRequestBody) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	e.p.HandlePDUSessionSMContextUpdate(c, body, e.sm.Ref)
	return rec
}

func relCompleteTestBody(t *testing.T, pti uint8) models.UpdateSmContextRequestBody {
	t.Helper()
	n1, err := (&message.PDUSessRelComplete{PDUSessId: 5, PTI: pti}).MarshalBinary()
	require.NoError(t, err)
	return models.UpdateSmContextRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextUpdateData{
			N1SmMsg: &models.RefToBinaryData{ContentId: "RelCompleteN1SmMsg"},
		},
		BinaryDataN1SmMessage: &multipart.RelatedContent{ContentID: "RelCompleteN1SmMsg", Content: n1},
	}
}

func relCompleteTestRemoved(ref string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if smf_context.GetSMContextByRef(ref) == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return smf_context.GetSMContextByRef(ref) == nil
}

// A PDU SESSION RELEASE COMPLETE outside a release (here before the gNB
// answered the Setup Request, so no AN tunnel is known yet) is answered with
// 5GSM STATUS #47 and leaves the established session and its N4 session as
// they are (TS 24.501 7.3.1 a).
func TestUnsolicitedReleaseCompleteKeepsSession(t *testing.T) {
	for _, state := range []smf_context.SMContextState{smf_context.Active, smf_context.ModificationPending} {
		t.Run(state.String(), func(t *testing.T) {
			e := newRelCompleteTestEnv(t)
			e.sm.SetState(state)

			rec := e.update(t, relCompleteTestBody(t, 0))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.False(t, relCompleteTestRemoved(e.sm.Ref, 100*time.Millisecond),
				"SM context removed (%d PFCP Session Deletion Requests)", e.deletions.Load())
			require.Equal(t, state, e.sm.State())
			require.Zero(t, e.deletions.Load(), "N4 session deleted")
			require.False(t, e.sm.PFCPReleaseDone)

			var rsp models.UpdateSmContextResponse200
			require.NoError(t, openapi.Deserialize(&rsp, rec.Body.Bytes(), rec.Header().Get("Content-Type")))
			require.NotNil(t, rsp.BinaryDataN1SmMessage, "no 5GSM STATUS in the response")
			gsm, err := message.ParseGSM(rsp.BinaryDataN1SmMessage.Content)
			require.NoError(t, err)
			status, ok := gsm.(*message.Status5GSM)
			require.True(t, ok, "N1 part is %T", gsm)
			require.Equal(t, nasie.Cause5GSM_PTIMismatch, status.Cause5GSM.Value)
			require.Equal(t, uint8(0), status.PTI)
			require.Equal(t, uint8(5), status.PDUSessId)
		})
	}
}

// Control, normal UE-requested release: the Release Complete that arrives
// before the gNB's PDU Session Resource Release Response must not remove the
// SM context yet (TS 23.502 4.3.4.2 steps 8-11); the Release Response does.
func TestReleaseCompleteBeforeN2ReleaseResponse(t *testing.T) {
	e := newRelCompleteTestEnv(t)
	e.sm.Tunnel.ANInformation.IPAddress, e.sm.Tunnel.ANInformation.TEID = net.ParseIP("10.100.0.5").To4(), 0x42
	e.sm.PFCPReleaseDone = true
	e.sm.SetState(smf_context.InActivePending)

	rec := e.update(t, relCompleteTestBody(t, 2))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "GSM_NAS", "no 5GSM STATUS for a solicited Release Complete")
	require.False(t, relCompleteTestRemoved(e.sm.Ref, 100*time.Millisecond), "removed before the N2 Release Response")
	require.Equal(t, smf_context.InActive, e.sm.State())

	rec = e.update(t, models.UpdateSmContextRequestBody{JsonData: &models.Smf_PDUSess_SmContextUpdateData{
		N2SmInfoType: models.Smf_PDUSess_N2SmInfoType_PDU_RES_REL_RSP,
	}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, relCompleteTestRemoved(e.sm.Ref, time.Second))
	require.Zero(t, e.deletions.Load())
}

// Control: a Release Complete that completes a release whose N2 side is done
// (InActivePending) or that answers a Release Command sent while the SMF kept
// the context InActive (association.go) removes the SM context.
func TestReleaseCompleteCompletesRelease(t *testing.T) {
	for _, state := range []smf_context.SMContextState{smf_context.InActivePending, smf_context.InActive} {
		t.Run(state.String(), func(t *testing.T) {
			e := newRelCompleteTestEnv(t)
			e.sm.PFCPReleaseDone = true
			e.sm.SetState(state)

			rec := e.update(t, relCompleteTestBody(t, 0))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.True(t, relCompleteTestRemoved(e.sm.Ref, time.Second))
			require.Zero(t, e.deletions.Load())
		})
	}
}
