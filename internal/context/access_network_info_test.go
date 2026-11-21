package context_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
)

var (
	aniTestServingNetwork = &models.PlmnIdNid{Mcc: "208", Mnc: "93"}
	aniTestUeLocation     = &models.UserLocation{NrLocation: &models.NrLocation{
		Tai:                      &models.Tai{PlmnId: &models.PlmnId{Mcc: "208", Mnc: "93"}, Tac: "000001"},
		AgeOfLocationInformation: 3,
	}}
)

func newAniTestSMContext(ueLocation *models.UserLocation) *smf_context.SMContext {
	return &smf_context.SMContext{Smf_PDUSess_SmContextCreateData: &models.Smf_PDUSess_SmContextCreateData{
		ServingNetwork: aniTestServingNetwork,
		UeLocation:     ueLocation,
		UeTimeZone:     "+08:00",
	}}
}

func anInfoDecision(reqData ...models.Pcf_SMPolCtrl_RequestedRuleDataType) *models.Pcf_SMPolCtrl_SmPolicyDecision {
	return &models.Pcf_SMPolCtrl_SmPolicyDecision{
		PolicyCtrlReqTriggers: []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
			models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_AN_INFO,
		},
		LastReqRuleData: []models.Pcf_SMPolCtrl_RequestedRuleData{{
			RefPccRuleIds: []string{"PccRuleId-1"},
			ReqData:       reqData,
		}},
	}
}

func TestRecordSmPolicyDecision(t *testing.T) {
	smContext := newAniTestSMContext(nil)
	require.False(t, smContext.NetLocSupported())

	// Decision creating the SM policy association: negotiated features
	smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{SuppFeat: "2f"})
	require.True(t, smContext.NetLocSupported())
	require.False(t, smContext.AnInfoTriggerSet)

	smContext.RecordSmPolicyDecision(anInfoDecision())
	require.True(t, smContext.AnInfoTriggerSet)
	require.True(t, smContext.NetLocSupported(), "features are kept when not provided again")

	// Triggers not provided: unchanged
	smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{})
	require.True(t, smContext.AnInfoTriggerSet)

	// Triggers provided again without AN_INFO
	smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{
		PolicyCtrlReqTriggers: []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
			models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_PLMN_CH,
		},
	})
	require.False(t, smContext.AnInfoTriggerSet)

	smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{SuppFeat: "0f"})
	require.False(t, smContext.NetLocSupported())
}

func TestAccessNetworkInfoReport(t *testing.T) {
	anInfo := []models.Pcf_SMPolCtrl_PolicyControlRequestTrigger{
		models.Pcf_SMPolCtrl_PolicyControlRequestTrigger_AN_INFO,
	}
	testCases := []struct {
		name       string
		suppFeat   string
		ueLocation *models.UserLocation
		decision   *models.Pcf_SMPolCtrl_SmPolicyDecision
		want       *models.Pcf_SMPolCtrl_SmPolicyUpdateContextData
	}{
		{
			name:       "user location and time zone",
			suppFeat:   smf_context.SmPolicySuppFeat,
			ueLocation: aniTestUeLocation,
			decision: anInfoDecision(models.Pcf_SMPolCtrl_RequestedRuleDataType_USER_LOC_INFO,
				models.Pcf_SMPolCtrl_RequestedRuleDataType_MS_TIME_ZONE),
			want: &models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{
				RepPolicyCtrlReqTriggers: anInfo,
				UserLocationInfo:         aniTestUeLocation,
				UeTimeZone:               "+08:00",
			},
		},
		{
			name:     "user location not available",
			suppFeat: smf_context.SmPolicySuppFeat,
			decision: anInfoDecision(models.Pcf_SMPolCtrl_RequestedRuleDataType_USER_LOC_INFO),
			want: &models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{
				RepPolicyCtrlReqTriggers: anInfo,
				ServingNetwork:           aniTestServingNetwork,
			},
		},
		{
			name:       "time zone only",
			suppFeat:   smf_context.SmPolicySuppFeat,
			ueLocation: aniTestUeLocation,
			decision:   anInfoDecision(models.Pcf_SMPolCtrl_RequestedRuleDataType_MS_TIME_ZONE),
			want: &models.Pcf_SMPolCtrl_SmPolicyUpdateContextData{
				RepPolicyCtrlReqTriggers: anInfo,
				UeTimeZone:               "+08:00",
			},
		},
		{
			name:       "no access network information requested",
			suppFeat:   smf_context.SmPolicySuppFeat,
			ueLocation: aniTestUeLocation,
			decision:   anInfoDecision(models.Pcf_SMPolCtrl_RequestedRuleDataType_SUCC_RES_ALLO),
		},
		{
			name:       "NetLoc not negotiated",
			suppFeat:   "0f",
			ueLocation: aniTestUeLocation,
			decision:   anInfoDecision(models.Pcf_SMPolCtrl_RequestedRuleDataType_USER_LOC_INFO),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			smContext := newAniTestSMContext(tc.ueLocation)
			smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{SuppFeat: tc.suppFeat})
			smContext.RecordSmPolicyDecision(tc.decision)
			require.Equal(t, tc.want, smContext.AccessNetworkInfoReport(tc.decision))
		})
	}

	t.Run("AN_INFO trigger not set", func(t *testing.T) {
		smContext := newAniTestSMContext(aniTestUeLocation)
		smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{SuppFeat: smf_context.SmPolicySuppFeat})
		decision := anInfoDecision(models.Pcf_SMPolCtrl_RequestedRuleDataType_USER_LOC_INFO)
		decision.PolicyCtrlReqTriggers = nil
		require.Nil(t, smContext.AccessNetworkInfoReport(decision))
	})
}

func TestSmPolicyDeleteData(t *testing.T) {
	smContext := newAniTestSMContext(aniTestUeLocation)
	require.Equal(t, models.Pcf_SMPolCtrl_SmPolicyDeleteData{}, smContext.SmPolicyDeleteData(),
		"nothing without NetLoc")

	smContext.RecordSmPolicyDecision(&models.Pcf_SMPolCtrl_SmPolicyDecision{SuppFeat: smf_context.SmPolicySuppFeat})
	require.Equal(t, models.Pcf_SMPolCtrl_SmPolicyDeleteData{
		UserLocationInfo: aniTestUeLocation,
		UeTimeZone:       "+08:00",
	}, smContext.SmPolicyDeleteData())

	smContext.UeLocation = nil
	require.Equal(t, models.Pcf_SMPolCtrl_SmPolicyDeleteData{
		ServingNetwork: aniTestServingNetwork,
		UeTimeZone:     "+08:00",
	}, smContext.SmPolicyDeleteData())
}

func TestUpdateAccessNetworkInfo(t *testing.T) {
	smContext := newAniTestSMContext(aniTestUeLocation)

	// Not provided: the last known information is kept.
	smContext.UpdateAccessNetworkInfo(nil, "")
	require.Equal(t, aniTestUeLocation, smContext.UeLocation)
	require.Equal(t, "+08:00", smContext.UeTimeZone)

	newLocation := &models.UserLocation{NrLocation: &models.NrLocation{
		Tai: &models.Tai{PlmnId: &models.PlmnId{Mcc: "208", Mnc: "93"}, Tac: "000002"},
	}}
	smContext.UpdateAccessNetworkInfo(newLocation, "+09:00")
	require.Equal(t, newLocation, smContext.UeLocation)
	require.Equal(t, "+09:00", smContext.UeTimeZone)

	// No create data: nothing to update, no panic.
	empty := &smf_context.SMContext{}
	empty.UpdateAccessNetworkInfo(newLocation, "+09:00")
	require.Nil(t, empty.PolicyUserLocation())
	require.Empty(t, empty.PolicyUeTimeZone())
	require.Nil(t, empty.PolicyServingNetwork())
}

func TestPolicyUserLocationNormalizesAge(t *testing.T) {
	smContext := newAniTestSMContext(&models.UserLocation{NrLocation: &models.NrLocation{
		AgeOfLocationInformation: -1,
	}})
	ueLocation := smContext.PolicyUserLocation()
	require.Equal(t, int32(0), ueLocation.NrLocation.AgeOfLocationInformation)
	require.Equal(t, int32(-1), smContext.UeLocation.NrLocation.AgeOfLocationInformation,
		"the SM context keeps the received location")
}
