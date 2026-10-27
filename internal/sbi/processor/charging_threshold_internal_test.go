package processor

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
)

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
