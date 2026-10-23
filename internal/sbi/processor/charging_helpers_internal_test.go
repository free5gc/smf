package processor

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

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

func recoverChargingNow(p *Processor) {
	p.recoverPendingCharging(context.Background(), time.Now().Add(time.Hour))
}
