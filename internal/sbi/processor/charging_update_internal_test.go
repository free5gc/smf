package processor

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

func TestChargingUpdateFailureRetainsUsage(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		chargingReleaseOrderFailure(t, w)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	p.ReportUsageAndUpdateQuota(sm)
	require.Len(t, sm.UrrReports, 1, "CHF failure must not discard usage")
	require.Equal(t, uint64(40), sm.UrrReports[0].TotalVolume)
	require.Equal(t, smf_context.Active, sm.State(), "no quota should be applied after CHF failure")
	require.Equal(t, int32(1), calls.Load())
}

func TestChargingUpdateFailureBlocksLaterAccounting(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	sm.SMPolicyID = chargingReleaseOrderPolicyID
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		chargingReleaseOrderFailure(t, w)
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	sm.SelectedPCFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NPCF_SMPOLICYCONTROL, server.URL)
	p.ReportUsageAndUpdateQuota(sm)
	state, firstErr := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateFailed, state)
	require.Error(t, firstErr)
	ref, pending, err := sm.PendingChargingRequest(smf_context.CHARGING_UPDATE)
	require.NoError(t, err)
	require.NotNil(t, pending)
	original, err := json.Marshal(pending)
	require.NoError(t, err)

	sm.UrrReports = append(sm.UrrReports, smf_context.UsageReport{
		UrrId: 1, UpfId: "upf-a", TotalVolume: 20,
		ReportTpye: models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED,
	})
	sm.SNssai.Sd = "ffffff"
	p.ReportUsageAndUpdateQuota(sm)
	p.UpdateChargingSession(sm, []*smf_context.URR{{URRID: 1}}, models.Chf_ConvCharging_Trigger{
		TriggerType: models.Chf_ConvCharging_TriggerType_ADDITION_OF_UPF,
	})
	require.Error(t, p.ReleaseChargingSession(sm))
	p.RemoveSMContextFromAllNF(sm, false)
	require.Same(t, sm, smf_context.GetSMContextByRef(sm.Ref))
	require.Equal(t, chargingReleaseOrderPolicyID, sm.SMPolicyID)
	require.Len(t, sm.UrrReports, 2)
	require.Equal(t, int32(1), calls.Load(), "failed usage must not be sent again under a new sequence")
	refAgain, pendingAgain, err := sm.PendingChargingRequest(smf_context.CHARGING_UPDATE)
	require.NoError(t, err)
	require.Equal(t, ref, refAgain)
	stillPending, err := json.Marshal(pendingAgain)
	require.NoError(t, err)
	require.JSONEq(t, string(original), string(stillPending))
}

func TestChargingUpdateSuccessConsumesAcknowledgedUsage(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	received := make(chan models.Chf_ConvCharging_ChargingDataRequest, 2)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request models.Chf_ConvCharging_ChargingDataRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode update request: %v", err)
		}
		received <- request
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(models.Chf_ConvCharging_ChargingDataResponse{
			InvocationSequenceNumber: request.InvocationSequenceNumber,
		}); err != nil {
			t.Errorf("write update response: %v", err)
		}
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	for index, volume := range []uint64{40, 60} {
		sm.UrrReports = []smf_context.UsageReport{{
			UrrId: 1, UpfId: "upf-a", TotalVolume: volume,
			ReportTpye: models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED,
		}}
		p.ReportUsageAndUpdateQuota(sm)
		request := <-received
		require.Equal(t, int32(index+1), request.InvocationSequenceNumber)
		require.Equal(t, int32(volume), request.MultipleUnitUsage[0].UsedUnitContainer[0].TotalVolume)
		require.Empty(t, sm.UrrReports)
		_, pending, err := sm.PendingChargingRequest(smf_context.CHARGING_UPDATE)
		require.NoError(t, err)
		require.Nil(t, pending)
		state, lastErr := sm.ChargingUpdateResult()
		require.Equal(t, smf_context.ChargingUpdateIdle, state)
		require.NoError(t, lastErr)
		require.Equal(t, smf_context.Active, sm.State(), "no PFCP work must leave the session active")
	}
}

func TestChargingUpdateTransportAndMalformedBodyRetainRequest(t *testing.T) {
	for _, name := range []string{"connection failure", "malformed response"} {
		t.Run(name, func(t *testing.T) {
			p, sm := chargingReleaseOrderSession(t)
			server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte("{")); err != nil {
					t.Errorf("write malformed response: %v", err)
				}
			})
			if name == "connection failure" {
				server.Close()
			}
			sm.SelectedCHFProfile = chargingReleaseOrderProfile(
				models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
			require.NotPanics(t, func() { p.ReportUsageAndUpdateQuota(sm) })
			require.Len(t, sm.UrrReports, 1)
			state, lastErr := sm.ChargingUpdateResult()
			require.Equal(t, smf_context.ChargingUpdateFailed, state)
			require.Error(t, lastErr)
			_, pending, err := sm.PendingChargingRequest(smf_context.CHARGING_UPDATE)
			require.NoError(t, err)
			require.NotNil(t, pending)
		})
	}
}

func TestChargingUpdateInProgressBlocksOtherAccounting(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	started, unblock := make(chan struct{}), make(chan struct{})
	allowResponse := sync.OnceFunc(func() { close(unblock) })
	defer allowResponse()
	var calls atomic.Int32
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request models.Chf_ConvCharging_ChargingDataRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode charging request: %v", err)
		}
		if calls.Add(1) == 1 {
			close(started)
			<-unblock
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(models.Chf_ConvCharging_ChargingDataResponse{
			InvocationSequenceNumber: request.InvocationSequenceNumber,
		}); err != nil {
			t.Errorf("write update response: %v", err)
		}
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	done := make(chan struct{})
	go func() {
		p.ReportUsageAndUpdateQuota(sm)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("CHF did not receive update")
	}
	require.Len(t, sm.UrrReports, 1, "usage must remain while waiting for CHF")
	state, lastErr := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateInProgress, state)
	require.NoError(t, lastErr)
	require.Error(t, p.ReleaseChargingSession(sm))
	p.ReportUsageAndUpdateQuota(sm)
	require.Equal(t, int32(1), calls.Load())
	allowResponse()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("update did not finish")
	}
	require.Empty(t, sm.UrrReports)
	require.True(t, sm.BeginChargingRelease(), "successful update must unblock final settlement")
	require.Error(t, sm.BeginChargingUpdate(), "updates must stop once release begins")
}

func TestChargingUpdateTriggerDoesNotConsumeUnreportedUsage(t *testing.T) {
	p, sm := chargingReleaseOrderSession(t)
	server := chargingReleaseOrderServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request models.Chf_ConvCharging_ChargingDataRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode charging request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(models.Chf_ConvCharging_ChargingDataResponse{
			InvocationSequenceNumber: request.InvocationSequenceNumber,
		}); err != nil {
			t.Errorf("write update response: %v", err)
		}
	})
	sm.SelectedCHFProfile = chargingReleaseOrderProfile(
		models.Nrf_NFMgmt_ServiceName_NCHF_CONVERGEDCHARGING, server.URL)
	p.UpdateChargingSession(sm, []*smf_context.URR{{URRID: 1}}, models.Chf_ConvCharging_Trigger{
		TriggerType: models.Chf_ConvCharging_TriggerType_ADDITION_OF_UPF,
	})
	require.Len(t, sm.UrrReports, 1, "a trigger-only update did not report this usage")
	require.Equal(t, uint64(40), sm.UrrReports[0].TotalVolume)
	state, lastErr := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
	require.NoError(t, lastErr)
}
