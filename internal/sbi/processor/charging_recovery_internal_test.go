package processor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func chargingSuccess(t *testing.T, w http.ResponseWriter, request *models.Chf_ConvCharging_ChargingDataRequest,
	infos ...models.Chf_ConvCharging_MultipleUnitInformation,
) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(models.Chf_ConvCharging_ChargingDataResponse{
		InvocationSequenceNumber: request.InvocationSequenceNumber, MultipleUnitInformation: infos,
	}))
}

func pendingChargingWork(t *testing.T, p *Processor, sm *smf_context.SMContext) *chargingWork {
	t.Helper()
	value, ok := p.chargingWork.Load(sm.Ref)
	require.True(t, ok)
	return value.(*chargingWork)
}

func recoverChargingNow(p *Processor) {
	p.recoverPendingCharging(context.Background(), time.Now().Add(time.Hour))
}

func TestChargingRecoveryRetriesExactRequestAndPreservesNewUsage(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	var calls atomic.Int32
	bodies := make(chan []byte, 2)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies <- body
		if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		var request models.Chf_ConvCharging_ChargingDataRequest
		require.NoError(t, json.Unmarshal(body, &request))
		chargingSuccess(t, w, &request)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	p.ReportUsageAndUpdateQuota(sm)
	work := pendingChargingWork(t, p, sm)
	require.True(t, work.retryAt.After(time.Now()))
	// The persisted body/destination must survive changes to discovery and metadata.
	sm.SelectedCHFProfile = models.Nrf_NFDisc_NFProfile{}
	sm.SNssai.Sd = "abcdef"
	extra := smf_context.UsageReport{
		UrrId: 1, UpfId: "upf-a", TotalVolume: 20,
		ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL,
	}
	sm.UrrReports = append(sm.UrrReports, extra)
	p.recoverPendingCharging(context.Background(), time.Now())
	require.Equal(t, int32(1), calls.Load(), "honor backoff")
	recoverChargingNow(p)
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, <-bodies, <-bodies, "sequence, timestamp, metadata and usage remain byte-identical")
	require.Equal(t, []smf_context.UsageReport{extra}, sm.UrrReports)
	state, lastErr := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
	require.NoError(t, lastErr)
	_, exists := p.chargingWork.Load(sm.Ref)
	require.False(t, exists)
	require.Equal(t, int32(2), sm.NextChargingSequenceNumber())
}

func TestChargingRecoveryBlocksPermanentErrorsAndWrongSequence(t *testing.T) {
	for _, status := range []int{400, 409, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			p, sm := chargingReleaseOrderSession(t)
			var calls atomic.Int32
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if status == 200 {
					chargingSuccess(t, w, &models.Chf_ConvCharging_ChargingDataRequest{InvocationSequenceNumber: 99})
					return
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(status)
				require.NoError(t, json.NewEncoder(w).Encode(models.ProblemDetails{
					Status: int32(status), Cause: "CHARGING_FAILED",
				}))
			})
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			p.ReportUsageAndUpdateQuota(sm)
			recoverChargingNow(p)
			require.Equal(t, int32(1), calls.Load())
			require.Len(t, sm.UrrReports, 1)
			work := pendingChargingWork(t, p, sm)
			require.False(t, work.retryable)
			if status == 200 {
				_, err := sm.ChargingUpdateResult()
				require.ErrorIs(t, err, smf_context.ErrChargingResponseSequence)
			}
		})
	}
}

func TestChargingRecoveryReleaseUsesOriginalRequest(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	sm.SMPolicyID = chargingReleaseOrderPolicyID
	var calls atomic.Int32
	bodies := make(chan []byte, 2)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies <- body
		if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	require.Error(t, p.ReleaseChargingSession(sm))
	require.Error(t, p.ReleaseChargingSession(sm), "ordinary release callers cannot send concurrently")
	recoverChargingNow(p)
	require.NoError(t, p.ReleaseChargingSession(sm))
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, <-bodies, <-bodies)
	require.Empty(t, sm.UrrReports)
	require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID,
		"settlement alone must wait for lifecycle authorization to remove the context")
}

type chargingPFCPClient struct {
	fakeActivePFCPClient
	modify func(*message.SessionModificationRequest, *net.UDPAddr, uint64) (*message.SessionModificationResponse, error)
}

func (c *chargingPFCPClient) SendSessionModificationRequest(
	_ context.Context, request *message.SessionModificationRequest, addr *net.UDPAddr, localSEID uint64,
) (*message.SessionModificationResponse, error) {
	return c.modify(request, addr, localSEID)
}

func quotaChargingSession(t *testing.T, count int) (*Processor, *smf_context.SMContext, []*smf_context.UPF) {
	t.Helper()
	p, sm := chargingReleaseOrderSession(t)
	self := smf_context.GetSelf()
	previous := self.ExternalAddr
	self.ExternalAddr = "127.0.0.1"
	t.Cleanup(func() { self.ExternalAddr = previous })
	sm.PFCPReleaseDone = false
	sm.RequestedUnit = 100
	sm.UrrReports = nil
	sm.ChargingInfo = make(map[uint32]*smf_context.ChargingInfo)
	var upfs []*smf_context.UPF
	for i := 0; i < count; i++ {
		node := pfcptype.NodeID{NodeIdType: pfcptype.NodeIdTypeIpv4Address, IP: net.IPv4(192, 0, 2, byte(210+i)).To4()}
		upf := smf_context.NewUPF(&node, nil)
		upf.EstablishAssociation(context.Background())
		t.Cleanup(func() { upf.CancelAssociation(); smf_context.RemoveUPFNodeByNodeID(node) })
		upfs = append(upfs, upf)
		id := uint32(i + 1)
		rule := &smf_context.URR{URRID: id, MeasureMethod: smf_context.MesureMethodVol}
		sm.UrrTable[upf.UUID()] = map[uint32]*smf_context.UrrEntry{
			id: {Rule: rule, RefCount: 1, State: smf_context.RULE_CREATE},
		}
		sm.ChargingInfo[id] = &smf_context.ChargingInfo{
			UpfId: upf.UUID(), RatingGroup: 10,
			ChargingMethod: models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING,
		}
		sm.PFCPContext[node.IP.String()] = &smf_context.PFCPSessionContext{
			LocalSEID: uint64(100 + i), RemoteSEID: uint64(200 + i),
		}
		sm.UrrReports = append(sm.UrrReports, smf_context.UsageReport{
			UrrId: id, UpfId: upf.UUID(), TotalVolume: 40,
			ReportTpye: models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED,
		})
	}
	return p, sm, upfs
}

func quotaInfo(upf string) models.Chf_ConvCharging_MultipleUnitInformation {
	return models.Chf_ConvCharging_MultipleUnitInformation{
		UPFID: upf, RatingGroup: 10,
		GrantedUnit: &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 100}, ValidityTime: 60,
		Triggers: []models.Chf_ConvCharging_Trigger{
			{TriggerType: models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED},
			{TriggerType: models.Chf_ConvCharging_TriggerType_VALIDITY_TIME},
		},
	}
}

func TestChargingQuotaRecoveryContinuesOnlyUnsentUPF(t *testing.T) {
	p, sm, upfs := quotaChargingSession(t, 2)
	var chfCalls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		chfCalls.Add(1)
		var request models.Chf_ConvCharging_ChargingDataRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		chargingSuccess(t, w, &request, quotaInfo(upfs[0].UUID()), quotaInfo(upfs[1].UUID()))
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	counts := make(map[string]int)
	client := &chargingPFCPClient{}
	client.modify = func(request *message.SessionModificationRequest, addr *net.UDPAddr, local uint64) (
		*message.SessionModificationResponse, error,
	) {
		counts[addr.IP.String()]++
		require.Len(t, request.UpdateURR, 1)
		quota, err := request.UpdateURR[0].VolumeQuota()
		require.NoError(t, err)
		require.Equal(t, uint64(100), quota.TotalVolume)
		p.SetActivePFCPClient(nil) // The other UPF fails before entering any transport.
		return message.NewSessionModificationResponse(
			0, 0, local, request.Sequence(), 0, ie.NewCause(ie.CauseRequestAccepted)), nil
	}
	p.SetActivePFCPClient(client)
	p.ReportUsageAndUpdateQuota(sm)
	state, lastErr := sm.ChargingUpdateResult()
	require.Error(t, lastErr)
	require.Equal(t, smf_context.ChargingUpdateQuotaPending, state)
	require.False(t, sm.BeginChargingRelease(), "pending quota must block final settlement")
	work := pendingChargingWork(t, p, sm)
	require.NotEmpty(t, work.response)
	require.True(t, work.consumed)
	require.Empty(t, sm.UrrReports)
	dates := make(map[string]time.Time)
	for _, upf := range upfs {
		for _, entry := range sm.UrrTable[upf.UUID()] {
			dates[upf.UUID()] = entry.Rule.QuotaValidityTime
		}
	}
	sm.UrrReports = append(sm.UrrReports, smf_context.UsageReport{UrrId: 1, UpfId: upfs[0].UUID(), TotalVolume: 20})
	p.SetActivePFCPClient(client)
	recoverChargingNow(p)
	require.Equal(t, int32(1), chfCalls.Load(), "cached CHF reply must not trigger accounting again")
	for _, upf := range upfs {
		require.Equal(t, 1, counts[upf.NodeID.IP.String()])
		for _, entry := range sm.UrrTable[upf.UUID()] {
			require.Equal(t, dates[upf.UUID()], entry.Rule.QuotaValidityTime)
		}
	}
	state, lastErr = sm.ChargingUpdateResult()
	require.NoError(t, lastErr)
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
	require.Len(t, sm.UrrReports, 1, "new reports survive quota-delivery retries")
}

func TestChargingQuotaUnknownOutcomeDoesNotResetQuota(t *testing.T) {
	p, sm, upfs := quotaChargingSession(t, 1)
	var chfCalls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		chfCalls.Add(1)
		var request models.Chf_ConvCharging_ChargingDataRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		chargingSuccess(t, w, &request, quotaInfo(upfs[0].UUID()))
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	var pfcpCalls int
	p.SetActivePFCPClient(&chargingPFCPClient{modify: func(*message.SessionModificationRequest, *net.UDPAddr, uint64) (
		*message.SessionModificationResponse, error,
	) {
		pfcpCalls++
		return nil, errors.New("accepted by UPF but acknowledgement lost")
	}})
	p.ReportUsageAndUpdateQuota(sm)
	work := pendingChargingWork(t, p, sm)
	require.NotEmpty(t, work.response)
	for range 3 {
		recoverChargingNow(p)
	}
	require.Equal(t, 1, pfcpCalls, "the existing transaction owns retransmission; never reset credit in a new transaction")
	require.Equal(t, int32(1), chfCalls.Load())
	state, lastErr := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateQuotaPending, state)
	require.Error(t, lastErr)
	require.False(t, sm.BeginChargingRelease(), "an unconfirmed quota must block final settlement")
	// A retired original rule needs no new grant and must not keep live owners blocked.
	sm.UrrTable[upfs[0].UUID()][1].RefCount = 0
	sm.SetUrrState(upfs[0].UUID(), 1, smf_context.RULE_REMOVE)
	recoverChargingNow(p)
	state, lastErr = sm.ChargingUpdateResult()
	require.NoError(t, lastErr)
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
	require.Equal(t, 1, pfcpCalls)
}

func TestChargingRecoveryNeverGrantsToReplacementRule(t *testing.T) {
	p, sm, upfs := quotaChargingSession(t, 1)
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		var request models.Chf_ConvCharging_ChargingDataRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		chargingSuccess(t, w, &request, quotaInfo(upfs[0].UUID()))
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	p.ReportUsageAndUpdateQuota(sm)
	replacement := &smf_context.URR{URRID: 1, VolumeQuota: 777}
	sm.UrrTable[upfs[0].UUID()][1].Rule = replacement
	recoverChargingNow(p)
	require.Equal(t, uint64(777), replacement.VolumeQuota)
	state, lastErr := sm.ChargingUpdateResult()
	require.NoError(t, lastErr)
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
}

func TestChargingBackgroundWorkerSerializesAndStops(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		var request models.Chf_ConvCharging_ChargingDataRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		chargingSuccess(t, w, &request)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	p.ReportUsageAndUpdateQuota(sm)
	work := pendingChargingWork(t, p, sm)
	work.retryAt = time.Time{}
	sm.SMLock.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.RunChargingRecovery(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	p.recoverPendingCharging(ctx, time.Now().Add(time.Hour))
	require.Equal(t, int32(1), calls.Load(), "locked session is skipped")
	sm.SMLock.Unlock()
	require.Eventually(t, func() bool {
		state, err := sm.ChargingUpdateResult()
		return state == smf_context.ChargingUpdateIdle && err == nil
	},
		3*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("charging worker failed to stop")
	}
	require.Equal(t, int32(2), calls.Load())
}

func TestChargingRecoveryRejectsChangedPFCPBindingBeforeMutatingQuota(t *testing.T) {
	for _, change := range []string{"session", "association"} {
		t.Run(change, func(t *testing.T) {
			p, sm, upfs := quotaChargingSession(t, 1)
			var calls atomic.Int32
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					chargingReleaseOrderFailure(t, w)
					return
				}
				var request models.Chf_ConvCharging_ChargingDataRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				chargingSuccess(t, w, &request, quotaInfo(upfs[0].UUID()))
			})
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			p.ReportUsageAndUpdateQuota(sm)
			if change == "session" {
				sm.PFCPContext[upfs[0].NodeID.IP.String()].RemoteSEID++
			} else {
				upfs[0].EstablishAssociation(context.Background())
			}
			recoverChargingNow(p)
			rule := sm.UrrTable[upfs[0].UUID()][1].Rule
			require.Zero(t, rule.VolumeQuota, "a response for the old binding cannot modify its replacement")
			require.True(t, rule.QuotaValidityTime.IsZero())
			state, err := sm.ChargingUpdateResult()
			require.Equal(t, smf_context.ChargingUpdateQuotaPending, state)
			require.Error(t, err)
			require.NotEmpty(t, pendingChargingWork(t, p, sm).response)
		})
	}
}

func TestChargingQuotaDeliveryExcludesUnrelatedPendingRules(t *testing.T) {
	p, sm, upfs := quotaChargingSession(t, 2)
	sm.UrrReports = sm.UrrReports[:1]
	unrelated := sm.UrrTable[upfs[1].UUID()][2].Rule
	unrelated.VolumeQuota = 777
	sm.SetUrrState(upfs[1].UUID(), 2, smf_context.RULE_UPDATE)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request models.Chf_ConvCharging_ChargingDataRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		chargingSuccess(t, w, &request, quotaInfo(upfs[0].UUID()))
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	var calls int
	p.SetActivePFCPClient(&chargingPFCPClient{modify: func(request *message.SessionModificationRequest,
		addr *net.UDPAddr, local uint64,
	) (*message.SessionModificationResponse, error) {
		calls++
		require.True(t, addr.IP.Equal(upfs[0].NodeID.IP))
		require.Len(t, request.UpdateURR, 1)
		return message.NewSessionModificationResponse(
			0, 0, local, request.Sequence(), 0, ie.NewCause(ie.CauseRequestAccepted)), nil
	}})
	p.ReportUsageAndUpdateQuota(sm)
	require.Equal(t, 1, calls)
	require.Equal(t, uint64(777), unrelated.VolumeQuota)
	require.Equal(t, smf_context.RULE_UPDATE, sm.GetUrrState(upfs[1].UUID(), 2))
}

func TestChargingWorkerCancellationStopsInFlightHTTP(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	started := make(chan struct{})
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		close(started)
		<-r.Context().Done()
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	p.ReportUsageAndUpdateQuota(sm)
	pendingChargingWork(t, p, sm).retryAt = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.RunChargingRecovery(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("retry did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HTTP retry ignored shutdown")
	}
	state, err := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateFailed, state)
	require.Error(t, err)
	require.Len(t, sm.UrrReports, 1)
	_, request, snapshotErr := sm.PendingChargingRequest(smf_context.CHARGING_UPDATE)
	require.NoError(t, snapshotErr)
	require.NotNil(t, request)
}

func TestChargingReleaseErrorUnionNeverCompletesCleanup(t *testing.T) {
	for _, status := range []int{400, 404, 409} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			p, sm := chargingReleaseOrderSession(t)
			sm.SMPolicyID = chargingReleaseOrderPolicyID
			var calls atomic.Int32
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(status)
				require.NoError(t, json.NewEncoder(w).Encode(models.ProblemDetails{
					Status: int32(status), Cause: "CHARGING_FAILED",
				}))
			})
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			p.RemoveSMContextFromAllNF(sm, false)
			recoverChargingNow(p)
			state, err := sm.ChargingReleaseResult()
			require.Equal(t, smf_context.ChargingReleaseFailed, state)
			require.Error(t, err)
			require.Equal(t, int32(1), calls.Load())
			require.Len(t, sm.UrrReports, 1)
			require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
			require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
		})
	}
}

func TestChargingTriggerOnlyRecoveryPreservesULCLEstablishment(t *testing.T) {
	p, sm, upfs := quotaChargingSession(t, 1)
	rule := sm.UrrTable[upfs[0].UUID()][1].Rule
	rule.VolumeQuota = 42
	sm.PFCPContext[upfs[0].NodeID.IP.String()].RemoteSEID = 0
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			chargingReleaseOrderFailure(t, w)
			return
		}
		var request models.Chf_ConvCharging_ChargingDataRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Nil(t, request.MultipleUnitUsage[0].RequestedUnit)
		chargingSuccess(t, w, &request, models.Chf_ConvCharging_MultipleUnitInformation{
			RatingGroup: 10, UPFID: upfs[0].UUID(), GrantedUnit: &models.Chf_ConvCharging_GrantedUnit{},
		})
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	p.UpdateChargingSession(sm, []*smf_context.URR{rule}, models.Chf_ConvCharging_Trigger{
		TriggerType:     models.Chf_ConvCharging_TriggerType_ADDITION_OF_UPF,
		TriggerCategory: models.Chf_ConvCharging_TriggerCategory_IMMEDIATE_REPORT,
	})
	recoverChargingNow(p)
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, uint64(42), rule.VolumeQuota)
	require.Len(t, sm.UrrReports, 1)
	state, err := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
	require.NoError(t, err)
}
