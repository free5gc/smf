package processor_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/h2non/gock"
	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"
	"go.uber.org/mock/gomock"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/mediatype/multipart"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/processor"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/smf/pkg/service"
)

const (
	acceptTestSupi              = "imsi-208930000007487"
	acceptTestUPFIP             = "192.168.179.1"
	acceptTestAMF               = "http://127.0.0.18:8000"
	acceptTestRemoteSEID uint64 = 0x5eed
)

// acceptTestPFCPClient accepts every PFCP Session Establishment and records
// every PFCP Session Deletion so tests can observe the UPF-side cleanup.
type acceptTestPFCPClient struct {
	deleteErr error

	mu      sync.Mutex
	deleted []uint64
}

func (f *acceptTestPFCPClient) SendAssociationSetupRequest(
	context.Context, *net.UDPAddr,
) (*message.AssociationSetupResponse, error) {
	return nil, errors.New("unexpected Association Setup")
}

func (f *acceptTestPFCPClient) SendHeartbeatRequest(
	context.Context, *net.UDPAddr,
) (*message.HeartbeatResponse, error) {
	return nil, errors.New("unexpected Heartbeat")
}

func (f *acceptTestPFCPClient) SendSessionEstablishmentRequest(
	_ context.Context,
	request *message.SessionEstablishmentRequest,
	_ *net.UDPAddr,
	localSEID uint64,
) (*message.SessionEstablishmentResponse, error) {
	return message.NewSessionEstablishmentResponse(
		0, 0, localSEID, request.Sequence(), 0,
		ie.NewCause(ie.CauseRequestAccepted),
		ie.NewFSEID(acceptTestRemoteSEID, net.ParseIP(acceptTestUPFIP).To4(), nil),
	), nil
}

func (f *acceptTestPFCPClient) SendSessionDeletionRequest(
	_ context.Context,
	request *message.SessionDeletionRequest,
	_ *net.UDPAddr,
	localSEID uint64,
) (*message.SessionDeletionResponse, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, request.SEID())
	f.mu.Unlock()
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return message.NewSessionDeletionResponse(
		0, 0, localSEID, request.Sequence(), 0,
		ie.NewCause(ie.CauseRequestAccepted),
	), nil
}

func (f *acceptTestPFCPClient) deletedSEIDs() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.deleted...)
}

func setupAcceptTest(t *testing.T, pfcpClient *acceptTestPFCPClient) *processor.Processor {
	t.Helper()
	openapi.InterceptInnerHttp2Client(t, false)
	gock.Flush()
	t.Cleanup(gock.Flush)
	initConfig()
	initStubPFCP()
	smf_context.AllocateUPFID()
	for _, upfNode := range smf_context.GetSelf().UserPlaneInformation.UPFs {
		upfNode.UPF.EstablishAssociation(context.Background())
	}

	mockSmf := service.NewMockSmfAppInterface(gomock.NewController(t))
	smfConsumer, err := consumer.NewConsumer(mockSmf)
	require.NoError(t, err)
	p, err := processor.NewProcessor(mockSmf)
	require.NoError(t, err)
	service.SMF = mockSmf
	mockSmf.EXPECT().Context().Return(smf_context.GetSelf()).AnyTimes()
	mockSmf.EXPECT().Consumer().Return(smfConsumer).AnyTimes()
	p.SetActivePFCPClient(pfcpClient)

	initDiscUDMStubNRF()
	initDiscPCFStubNRF()
	initGetSMDataStubUDM()
	initSMPoliciesPostStubPCF()
	initDiscAMFStubNRF()
	return p
}

func acceptTestStatusURIPath(pduSessionID int32) string {
	return fmt.Sprintf("/namf-callback/v1/smContextStatus/%s/%d", acceptTestSupi, pduSessionID)
}

// createAcceptTestSMContext runs the SM Context Create handler. The handler
// hands SMLock to the establishment goroutine before it returns, so a later
// SMLock.Lock() in the test waits for that goroutine to finish.
func createAcceptTestSMContext(
	t *testing.T,
	p *processor.Processor,
	pduSessionID int32,
	isDone <-chan struct{},
) *smf_context.SMContext {
	t.Helper()
	snssaiInfo := testConfig.Configuration.SNssaiInfo[0]
	plmnID := &models.PlmnIdNid{Mcc: "208", Mnc: "93"}
	request := models.PostSmContextsRequestBody{
		JsonData: &models.Smf_PDUSess_SmContextCreateData{
			Supi:               acceptTestSupi,
			PduSessionId:       pduSessionID,
			Dnn:                snssaiInfo.DnnInfos[0].Dnn,
			SNssai:             snssaiInfo.SNssai,
			Guami:              &models.Guami{PlmnId: plmnID},
			AnType:             models.AccessType_3_GPP_ACCESS,
			ServingNetwork:     plmnID,
			SmContextStatusUri: acceptTestAMF + acceptTestStatusURIPath(pduSessionID),
		},
		BinaryDataN1SmMessage: &multipart.RelatedContent{
			Content: buildPDUSessionEstablishmentRequest(
				uint8(pduSessionID), uint8(pduSessionID), nasie.PDUSessType_IPv4),
		},
	}

	httpRecorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(httpRecorder)
	p.HandlePDUSessionSMContextCreate(c, request, isDone)
	require.Equal(t, http.StatusCreated, httpRecorder.Code)

	ref, err := smf_context.ResolveRef(acceptTestSupi, pduSessionID)
	require.NoError(t, err)
	smContext := smf_context.GetSMContextByRef(ref)
	require.NotNil(t, smContext)
	t.Cleanup(func() {
		smContext.SMLock.Lock()
		defer smContext.SMLock.Unlock()
		smf_context.RemoveSMContext(ref)
	})
	return smContext
}

func n1n2MessageTransferRequest() *gock.Request {
	return gock.New(acceptTestAMF).
		Post("/namf-comm/v1/ue-contexts/" + acceptTestSupi + "/n1-n2-messages")
}

func stubN1N2MessageTransfer(cause models.Amf_Comm_N1N2MessageTransferCause) *gock.Response {
	return n1n2MessageTransferRequest().
		Reply(http.StatusOK).
		JSON(models.Amf_Comm_N1N2MessageTransferRspData{Cause: cause})
}

func stubN1N2MessageTransferProblem(status int, cause string) *gock.Response {
	return n1n2MessageTransferRequest().
		Reply(status).
		JSON(models.ProblemDetails{Status: int32(status), Cause: cause})
}

func stubSMContextStatusNotify(pduSessionID int32) *gock.Response {
	return gock.New(acceptTestAMF).
		Post(acceptTestStatusURIPath(pduSessionID)).
		Reply(http.StatusNoContent)
}

func stubSMPolicyDelete() *gock.Response {
	return gock.New("http://127.0.0.7:8000" + factory.PcfSmpolicycontrolUriPrefix).
		Post("/sm-policies/" + acceptTestSupi + "-10/delete").
		Reply(http.StatusNoContent)
}

// waitForSMContextRelease waits until the SMContext leaves the pool and then
// takes SMLock so the release goroutine has finished before fields are read.
func waitForSMContextRelease(t *testing.T, smContext *smf_context.SMContext) {
	t.Helper()
	require.Eventually(t, func() bool {
		return smf_context.GetSMContextByRef(smContext.Ref) == nil
	}, 5*time.Second, 10*time.Millisecond, "SMContext was not removed from the pool")

	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()
	require.Equal(t, smf_context.InActive, smContext.State())
	require.Nil(t, smContext.SelectedUPF, "UE IP was not released")
}

func TestSendPDUSessionEstablishmentAcceptInvalidSMContextReleasesSMContext(t *testing.T) {
	pfcpClient := &acceptTestPFCPClient{}
	p := setupAcceptTest(t, pfcpClient)
	n1n2 := stubN1N2MessageTransferProblem(http.StatusForbidden, "INVALID_SM_CONTEXT")
	notify := stubSMContextStatusNotify(11)
	policyDelete := stubSMPolicyDelete()

	smContext := createAcceptTestSMContext(t, p, 11, nil)
	waitForSMContextRelease(t, smContext)

	require.True(t, n1n2.Done(), "N1N2MessageTransfer was not attempted")
	require.Equal(t, []uint64{acceptTestRemoteSEID}, pfcpClient.deletedSEIDs())
	require.Zero(t, smContext.PFCPContext[acceptTestUPFIP].RemoteSEID)
	require.True(t, policyDelete.Done(), "SM Policy Association was not terminated")
	require.False(t, notify.Done(), "SMContextStatusNotify was sent after INVALID_SM_CONTEXT")
}

func TestSendPDUSessionEstablishmentAcceptTokenFailureReleasesSMContext(t *testing.T) {
	pfcpClient := &acceptTestPFCPClient{}
	p := setupAcceptTest(t, pfcpClient)
	n1n2 := stubN1N2MessageTransfer(models.Amf_Comm_N1N2MessageTransferCause_N1_N2_TRANSFER_INITIATED)
	notify := stubSMContextStatusNotify(12)

	smfSelf := smf_context.GetSelf()
	t.Cleanup(func() { smfSelf.OAuth2Required = false })
	isDone := make(chan struct{})
	smContext := createAcceptTestSMContext(t, p, 12, isDone)

	// The establishment goroutine is parked on isDone, so OAuth2 is enabled
	// only for the Accept and the cleanup that follows it.
	smfSelf.OAuth2Required = true
	gock.New(smfSelf.NrfUri).
		Post("/oauth2/token").
		Persist().
		Reply(http.StatusInternalServerError).
		JSON(models.ProblemDetails{Status: http.StatusInternalServerError})
	close(isDone)

	waitForSMContextRelease(t, smContext)

	require.False(t, n1n2.Done(), "N1N2MessageTransfer was sent without an access token")
	require.Equal(t, []uint64{acceptTestRemoteSEID}, pfcpClient.deletedSEIDs())
	require.False(t, notify.Done(), "SMContextStatusNotify was sent without an access token")
}

func TestSendPDUSessionEstablishmentAcceptReleasesLocallyWhenPFCPDeletionFails(t *testing.T) {
	pfcpClient := &acceptTestPFCPClient{deleteErr: errors.New("UPF unreachable")}
	p := setupAcceptTest(t, pfcpClient)
	stubN1N2MessageTransferProblem(http.StatusForbidden, "INVALID_SM_CONTEXT")

	smContext := createAcceptTestSMContext(t, p, 13, nil)
	waitForSMContextRelease(t, smContext)

	require.Equal(t, []uint64{acceptTestRemoteSEID}, pfcpClient.deletedSEIDs())
}

func TestSendPDUSessionEstablishmentAcceptSuccessKeepsSMContext(t *testing.T) {
	pfcpClient := &acceptTestPFCPClient{}
	p := setupAcceptTest(t, pfcpClient)
	n1n2 := stubN1N2MessageTransfer(models.Amf_Comm_N1N2MessageTransferCause_N1_N2_TRANSFER_INITIATED)
	notify := stubSMContextStatusNotify(14)

	smContext := createAcceptTestSMContext(t, p, 14, nil)

	smContext.SMLock.Lock()
	state := smContext.State()
	remoteSEID := smContext.PFCPContext[acceptTestUPFIP].RemoteSEID
	smContext.SMLock.Unlock()

	require.True(t, n1n2.Done(), "N1N2MessageTransfer was not sent")
	require.Equal(t, smf_context.Active, state)
	require.Equal(t, acceptTestRemoteSEID, remoteSEID)
	require.Empty(t, pfcpClient.deletedSEIDs())
	require.Same(t, smContext, smf_context.GetSMContextByRef(smContext.Ref))
	require.False(t, notify.Done(), "AMF was told to release an established PDU Session")
}

func TestSendPDUSessionEstablishmentAcceptKeepsSMContextAfterN1N2Invoked(t *testing.T) {
	testCases := []struct {
		name         string
		pduSessionID int32
		stubN1N2     func() *gock.Response
		wantState    smf_context.SMContextState
	}{
		{
			name:         "N1_MSG_NOT_TRANSFERRED",
			pduSessionID: 1,
			stubN1N2: func() *gock.Response {
				return stubN1N2MessageTransfer(models.Amf_Comm_N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED)
			},
			wantState: smf_context.Active,
		},
		{
			name:         "transport error",
			pduSessionID: 2,
			stubN1N2: func() *gock.Response {
				return n1n2MessageTransferRequest().ReplyError(errors.New("connection reset by peer"))
			},
			wantState: smf_context.ActivePending,
		},
		{
			name:         "undecodable response",
			pduSessionID: 3,
			stubN1N2: func() *gock.Response {
				return n1n2MessageTransferRequest().
					Reply(http.StatusOK).
					SetHeader("Content-Type", "application/json").
					BodyString("{")
			},
			wantState: smf_context.ActivePending,
		},
		{
			name:         "other ProblemDetails cause",
			pduSessionID: 4,
			stubN1N2: func() *gock.Response {
				return stubN1N2MessageTransferProblem(http.StatusInternalServerError, "SYSTEM_FAILURE")
			},
			wantState: smf_context.ActivePending,
		},
		{
			name:         "other 403 cause",
			pduSessionID: 5,
			stubN1N2: func() *gock.Response {
				return stubN1N2MessageTransferProblem(http.StatusForbidden, "UE_IN_NON_ALLOWED_AREA")
			},
			wantState: smf_context.ActivePending,
		},
		{
			name:         "N1N2MessageTransferError",
			pduSessionID: 6,
			stubN1N2: func() *gock.Response {
				return n1n2MessageTransferRequest().
					Reply(http.StatusGatewayTimeout).
					JSON(models.Amf_Comm_N1N2MessageTransferError{
						Error: &models.ProblemDetails{
							Status: http.StatusGatewayTimeout,
							Cause:  "UE_NOT_REACHABLE",
						},
					})
			},
			wantState: smf_context.ActivePending,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pfcpClient := &acceptTestPFCPClient{}
			p := setupAcceptTest(t, pfcpClient)
			n1n2 := tc.stubN1N2()
			notify := stubSMContextStatusNotify(tc.pduSessionID)

			smContext := createAcceptTestSMContext(t, p, tc.pduSessionID, nil)

			smContext.SMLock.Lock()
			state := smContext.State()
			remoteSEID := smContext.PFCPContext[acceptTestUPFIP].RemoteSEID
			smContext.SMLock.Unlock()

			require.True(t, n1n2.Done(), "N1N2MessageTransfer was not sent")
			require.Equal(t, tc.wantState, state)
			require.Equal(t, acceptTestRemoteSEID, remoteSEID)
			require.Empty(t, pfcpClient.deletedSEIDs())
			require.Same(t, smContext, smf_context.GetSMContextByRef(smContext.Ref))
			require.False(t, notify.Done(), "AMF was told to release the PDU Session")
		})
	}
}
