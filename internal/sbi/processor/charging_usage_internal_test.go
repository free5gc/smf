package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

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
