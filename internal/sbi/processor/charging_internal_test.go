package processor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

// Quota calculation and PFCP delivery.

func TestChargingQuotaThresholdUsesRemainingTotalVolume(t *testing.T) {
	for _, tc := range []struct {
		name      string
		grant     *models.Chf_ConvCharging_GrantedUnit
		remaining int32
		consumed  uint64
		report    bool
		reverse   bool
	}{
		{
			name:      "remaining 800 of 1000",
			grant:     &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 1000},
			remaining: 800, consumed: 200, report: true,
		},
		{
			name:      "reversed triggers",
			grant:     &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 1000},
			remaining: 800, consumed: 200, report: true, reverse: true,
		},
		{
			name:      "small funded grant",
			grant:     &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 76},
			remaining: 60, consumed: 16, report: true,
		},
		{
			name:      "threshold equals grant",
			grant:     &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 1000},
			remaining: 1000, report: true,
		},
		{
			name:      "threshold exceeds grant",
			grant:     &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 76},
			remaining: 800, report: true,
		},
		{name: "threshold omitted", grant: &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 1000}},
		{name: "zero grant", grant: &models.Chf_ConvCharging_GrantedUnit{}, remaining: 800},
		{name: "no grant", remaining: 800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, sm, upfs := quotaChargingSession(t, 1)
			upf := upfs[0]
			rule := sm.UrrTable[upf.UUID()][1].Rule
			// A new response must replace a previously installed threshold.
			rule.VolumeThreshold = 999
			rule.ReportingTrigger.SetVOLTH()
			info := models.Chf_ConvCharging_MultipleUnitInformation{
				UPFID: upf.UUID(), RatingGroup: 10, GrantedUnit: tc.grant,
				VolumeQuotaThreshold: tc.remaining,
				Triggers: []models.Chf_ConvCharging_Trigger{
					{TriggerType: models.Chf_ConvCharging_TriggerType_QUOTA_THRESHOLD},
					{TriggerType: models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED},
				},
			}
			if tc.reverse {
				info.Triggers[0], info.Triggers[1] = info.Triggers[1], info.Triggers[0]
			}
			p.updateGrantedQuota(sm, []models.Chf_ConvCharging_MultipleUnitInformation{info})
			request, err := pfcp_message.BuildSessionModificationRequest(
				upf.NodeID.ResolveNodeIdToIp().String(), upf.UUID(), sm, nil, nil, nil, nil, []*smf_context.URR{rule})
			require.NoError(t, err)
			wire, err := request.Marshal()
			require.NoError(t, err)
			decoded, err := message.Parse(wire)
			require.NoError(t, err)
			parsed := decoded.(*message.SessionModificationRequest)
			require.Len(t, parsed.UpdateURR, 1)
			update := parsed.UpdateURR[0]
			quota, err := update.VolumeQuota()
			require.NoError(t, err, "a zero quota must be sent explicitly to stop forwarding")
			require.True(t, quota.HasTOVOL())
			require.False(t, quota.HasULVOL())
			require.False(t, quota.HasDLVOL())
			var granted uint64
			if tc.grant != nil {
				granted = uint64(tc.grant.TotalVolume)
			}
			require.Equal(t, granted, quota.TotalVolume)
			children, err := update.UpdateURR()
			require.NoError(t, err)
			var thresholdIE *ie.IE
			for _, child := range children {
				if child.Type == ie.ReportingTriggers {
					require.Equal(t, tc.report, child.HasVOLTH())
				}
				if child.Type == ie.VolumeThreshold {
					thresholdIE = child
				}
			}
			if !tc.report {
				require.Nil(t, thresholdIE)
				return
			}
			require.NotNil(t, thresholdIE, "an immediate zero threshold must not be omitted")
			threshold, err := thresholdIE.VolumeThreshold()
			require.NoError(t, err)
			require.True(t, threshold.HasTOVOL(), "the CHF grant is for combined UL+DL volume")
			require.False(t, threshold.HasULVOL())
			require.False(t, threshold.HasDLVOL())
			require.Equal(t, tc.consumed, threshold.TotalVolume)
		})
	}
}

func TestOfflineChargingThresholdKeepsUsageVolume(t *testing.T) {
	p, sm, upfs := quotaChargingSession(t, 1)
	upfID := upfs[0].UUID()
	sm.ChargingInfo[1].ChargingMethod = models.Chf_ConvCharging_QuotaManagementIndicator_OFFLINE_CHARGING
	rule := sm.UrrTable[upfID][1].Rule
	p.updateGrantedQuota(sm, []models.Chf_ConvCharging_MultipleUnitInformation{{
		UPFID: upfID, RatingGroup: 10, VolumeQuotaThreshold: 300,
		Triggers: []models.Chf_ConvCharging_Trigger{{TriggerType: models.Chf_ConvCharging_TriggerType_QUOTA_THRESHOLD}},
	}})
	require.True(t, rule.ReportingTrigger.HasVOLTH())
	require.Equal(t, uint64(300), rule.VolumeThreshold)
	require.Zero(t, rule.VolumeQuota)
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
	require.NotEmpty(t, work.chfResponse)
	require.True(t, work.usageConsumed)
	require.Empty(t, sm.UrrReports)
	dates := make(map[string]time.Time)
	for _, upf := range upfs {
		for _, entry := range sm.UrrTable[upf.UUID()] {
			dates[upf.UUID()] = entry.Rule.QuotaValidityTime
		}
	}
	sm.UrrReports = append(sm.UrrReports, smf_context.UsageReport{UrrId: 1, UpfId: upfs[0].UUID(), TotalVolume: 20})
	p.SetActivePFCPClient(client)
	retryChargingNow(p)
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
	require.NotEmpty(t, work.chfResponse)
	for range 3 {
		retryChargingNow(p)
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
	retryChargingNow(p)
	state, lastErr = sm.ChargingUpdateResult()
	require.NoError(t, lastErr)
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
	require.Equal(t, 1, pfcpCalls)
}

func TestChargingRetryNeverGrantsToReplacementRule(t *testing.T) {
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
	retryChargingNow(p)
	require.Equal(t, uint64(777), replacement.VolumeQuota)
	state, lastErr := sm.ChargingUpdateResult()
	require.NoError(t, lastErr)
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
}

func TestChargingRetryRejectsChangedPFCPBindingBeforeMutatingQuota(t *testing.T) {
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
			retryChargingNow(p)
			rule := sm.UrrTable[upfs[0].UUID()][1].Rule
			require.Zero(t, rule.VolumeQuota, "a response for the old binding cannot modify its replacement")
			require.True(t, rule.QuotaValidityTime.IsZero())
			state, err := sm.ChargingUpdateResult()
			require.Equal(t, smf_context.ChargingUpdateQuotaPending, state)
			require.Error(t, err)
			require.NotEmpty(t, pendingChargingWork(t, p, sm).chfResponse)
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
	retryChargingNow(p)
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, uint64(42), rule.VolumeQuota)
	require.Len(t, sm.UrrReports, 1)
	require.Equal(t, uint64(40), sm.UrrReports[0].TotalVolume)
	state, err := sm.ChargingUpdateResult()
	require.Equal(t, smf_context.ChargingUpdateIdle, state)
	require.NoError(t, err)
}

// Usage reporting and retired rules.

func TestRetiredTIUsageDoesNotRequestNewQuota(t *testing.T) {
	const upfID = "upf-ti"
	retired := &smf_context.URR{URRID: 10, VolumeQuota: 42}
	active := &smf_context.URR{URRID: 8}
	sm := &smf_context.SMContext{
		RequestedUnit: 4096,
		ChargingInfo: map[uint32]*smf_context.ChargingInfo{
			10: {RatingGroup: 6, UpfId: upfID, ChargingMethod: models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING},
			8:  {RatingGroup: 5, UpfId: upfID, ChargingMethod: models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING},
		},
		UrrTable: map[string]map[uint32]*smf_context.UrrEntry{
			upfID: {
				10: {Rule: retired, State: smf_context.RULE_REMOVE},
				8:  {Rule: active, State: smf_context.RULE_CREATE, RefCount: 2},
			},
		},
		UrrReports: []smf_context.UsageReport{
			{UrrId: 10, UpfId: upfID, ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL, TotalVolume: 1234},
			{UrrId: 8, UpfId: upfID, ReportTpye: models.Chf_ConvCharging_TriggerType_START_OF_SERVICE_DATA_FLOW},
		},
	}
	usages := buildMultiUnitUsage(sm, sm.UrrReports)
	require.Len(t, usages, 2)
	for _, usage := range usages {
		if usage.RatingGroup == 6 {
			require.Equal(t, int32(1234), usage.UsedUnitContainer[0].TotalVolume)
			require.Nil(t, usage.RequestedUnit, "final usage must be charged without requesting more credit")
		} else {
			require.NotNil(t, usage.RequestedUnit, "the live service still needs its initial quota")
			require.Equal(t, sm.RequestedUnit, usage.RequestedUnit.TotalVolume)
		}
	}
	processor := &Processor{}
	processor.updateGrantedQuota(sm, []models.Chf_ConvCharging_MultipleUnitInformation{{
		RatingGroup: 6, UPFID: upfID,
		GrantedUnit: &models.Chf_ConvCharging_GrantedUnit{TotalVolume: 9999},
		Triggers: []models.Chf_ConvCharging_Trigger{{
			TriggerType: models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED,
		}},
	}})
	require.Equal(t, uint64(42), retired.VolumeQuota)
	require.Equal(t, smf_context.RULE_REMOVE, sm.GetUrrState(upfID, retired.URRID))
	// A final report and a live report may share the same charging group.
	sm.ChargingInfo[10].RatingGroup = 5
	sm.UrrReports = []smf_context.UsageReport{
		{UrrId: 10, UpfId: upfID, ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL, TotalVolume: 1234},
		{UrrId: 8, UpfId: upfID, ReportTpye: models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED},
	}
	usages = buildMultiUnitUsage(sm, sm.UrrReports)
	require.Len(t, usages, 1)
	require.Len(t, usages[0].UsedUnitContainer, 2)
	require.NotNil(t, usages[0].RequestedUnit, "retiring TI must not suppress the live service's credit request")
}

func TestDeletingTIURRStopsTimersButKeepsFinalChargingIdentity(t *testing.T) {
	const upfID = "upf-ti"
	info := &smf_context.ChargingInfo{
		UpfId: upfID, RatingGroup: 6,
		ChargingMethod: models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING,
	}
	info.VolumeLimitExpiryTimer = smf_context.NewTimer(time.Hour, 1, func(int32) {}, func() {})
	info.EventLimitExpiryTimer = smf_context.NewTimer(time.Hour, 1, func(int32) {}, func() {})
	sm := &smf_context.SMContext{
		ChargingInfo: map[uint32]*smf_context.ChargingInfo{10: info},
		UrrTable: map[string]map[uint32]*smf_context.UrrEntry{
			upfID: {10: {Rule: &smf_context.URR{URRID: 10}, State: smf_context.RULE_REMOVE}},
		},
		UrrReports: []smf_context.UsageReport{{
			UrrId: 10, UpfId: upfID, ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL, TotalVolume: 300,
		}},
	}
	sm.DeleteFromUrrTable(upfID, 10)
	require.Nil(t, info.VolumeLimitExpiryTimer)
	require.Nil(t, info.EventLimitExpiryTimer)
	require.Same(t, info, sm.ChargingInfo[10])
	usages := buildMultiUnitUsage(sm, sm.UrrReports)
	require.Len(t, usages, 1)
	require.Equal(t, int32(300), usages[0].UsedUnitContainer[0].TotalVolume)
	require.Nil(t, usages[0].RequestedUnit)
	// A delayed CHF response must not recreate a quota or timer after deletion.
	(&Processor{}).updateGrantedQuota(sm, []models.Chf_ConvCharging_MultipleUnitInformation{{
		RatingGroup: 6, UPFID: upfID,
	}})
	require.Empty(t, sm.UrrTable[upfID])
}

func TestStaticAnchorsKeepSeparateChargingUPFIdentity(t *testing.T) {
	sm := &smf_context.SMContext{
		ChargingInfo: map[uint32]*smf_context.ChargingInfo{
			1: {RatingGroup: 5, UpfId: "old-anchor"},
			2: {RatingGroup: 5, UpfId: "mec-anchor"},
		},
		UrrReports: []smf_context.UsageReport{
			{UrrId: 1, UpfId: "old-anchor", ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL, TotalVolume: 100},
			{UrrId: 2, UpfId: "mec-anchor", ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL, TotalVolume: 200},
		},
	}
	usages := buildMultiUnitUsage(sm, sm.UrrReports)
	require.Len(t, usages, 2, "one rating group at two anchors must not lose its UPF identity")
	volumes := make(map[string]int32)
	for _, usage := range usages {
		require.Equal(t, int32(5), usage.RatingGroup)
		require.Len(t, usage.UsedUnitContainer, 1)
		volumes[usage.UPFID] = usage.UsedUnitContainer[0].TotalVolume
	}
	require.Equal(t, map[string]int32{"old-anchor": 100, "mec-anchor": 200}, volumes)
}

// Request replay and background retries.

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
	require.Equal(t, smf_context.Active, sm.State())
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

func TestChargingRetryRetriesExactRequestAndPreservesNewUsage(t *testing.T) {
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
	// The in-memory body/destination must survive changes to discovery and metadata.
	sm.SelectedCHFProfile = models.Nrf_NFDisc_NFProfile{}
	sm.SNssai.Sd = "abcdef"
	extra := smf_context.UsageReport{
		UrrId: 1, UpfId: "upf-a", TotalVolume: 20,
		ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL,
	}
	sm.UrrReports = append(sm.UrrReports, extra)
	p.retryPendingCharging(context.Background(), time.Now())
	require.Equal(t, int32(1), calls.Load(), "honor backoff")
	retryChargingNow(p)
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

func TestChargingRetryBlocksPermanentErrorsAndWrongSequence(t *testing.T) {
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
			retryChargingNow(p)
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
	go func() { defer close(done); p.RunChargingRetries(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	p.retryPendingCharging(ctx, time.Now().Add(time.Hour))
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
	go func() { defer close(done); p.RunChargingRetries(ctx) }()
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

// Shared charging fixtures.

const (
	chargingReleaseOrderPolicyID = "policy-1226"
	chargingAMFRelease           = "AMF release"
	chargingCommonCleanup        = "common cleanup"
	chargingReleasedPath         = "/released"
)

// PCF and CHF are local HTTP/2 stubs. The cached FINAL report represents
// an already deleted PFCP session whose charging data must survive settlement.
type chargingReleaseOrderApp struct {
	ctx      *smf_context.SMFContext
	consumer *consumer.Consumer
}

func (*chargingReleaseOrderApp) SetLogEnable(bool) {}

func (*chargingReleaseOrderApp) SetLogLevel(string) {}

func (*chargingReleaseOrderApp) SetReportCaller(bool) {}

func (*chargingReleaseOrderApp) Start() {}

func (*chargingReleaseOrderApp) Terminate() {}

func (*chargingReleaseOrderApp) Config() *factory.Config { return nil }

func (a *chargingReleaseOrderApp) Context() *smf_context.SMFContext { return a.ctx }

func (a *chargingReleaseOrderApp) Consumer() *consumer.Consumer { return a.consumer }

func chargingReleaseOrderSession(t *testing.T) (*Processor, *smf_context.SMContext) {
	t.Helper()
	self := smf_context.GetSelf()
	oldOAuth := self.OAuth2Required
	oldTEID := smf_context.TeidGenerator
	self.OAuth2Required = false
	smf_context.TeidGenerator = idgenerator.NewGenerator(1, math.MaxUint32)
	t.Cleanup(func() {
		self.OAuth2Required = oldOAuth
		smf_context.TeidGenerator = oldTEID
	})
	app := &chargingReleaseOrderApp{ctx: self}
	var err error
	app.consumer, err = consumer.NewConsumer(app)
	require.NoError(t, err)
	p, err := NewProcessor(app)
	require.NoError(t, err)
	sm := smf_context.NewSMContext("imsi-208930000001226", 1)
	require.NotNil(t, sm)
	t.Cleanup(func() { smf_context.RemoveSMContext(sm.Ref) })
	sm.Smf_PDUSess_SmContextCreateData = &models.Smf_PDUSess_SmContextCreateData{
		Supi: "imsi-208930000001226", PduSessionId: 1, Dnn: "internet",
		SNssai: &models.Snssai{Sst: 1, Sd: "010203"},
	}
	sm.SetState(smf_context.Active)
	sm.PFCPReleaseDone = true
	sm.ChargingDataRef = "charging-1226"
	sm.ChargingInfo[1] = &smf_context.ChargingInfo{
		RatingGroup: 10, UpfId: "upf-a",
		ChargingMethod: models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING,
	}
	sm.UrrReports = []smf_context.UsageReport{{
		UrrId: 1, UpfId: "upf-a", TotalVolume: 40,
		ReportTpye: models.Chf_ConvCharging_TriggerType_FINAL,
	}}
	return p, sm
}

func chargingReleaseOrderServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	s.Config.Protocols = protocols
	s.Start()
	t.Cleanup(s.Close)
	return s
}

func chargingReleaseOrderProfile(name models.Nrf_NFMgmt_ServiceName, root string) models.Nrf_NFDisc_NFProfile {
	return models.Nrf_NFDisc_NFProfile{NfServices: []models.Nrf_NFDisc_NFService{{
		ServiceName: name, ApiPrefix: root,
	}}}
}

func chargingReleaseOrderFailure(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusInternalServerError)
	body := []byte(`{"status":500,"cause":"SYSTEM_FAILURE","detail":"injected settlement failure"}`)
	if _, err := w.Write(body); err != nil {
		t.Errorf("write CHF error response: %v", err)
	}
}

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

func retryChargingNow(p *Processor) {
	p.retryPendingCharging(context.Background(), time.Now().Add(time.Hour))
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
