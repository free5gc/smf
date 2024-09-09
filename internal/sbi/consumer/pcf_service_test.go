package consumer_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/free5gc/nas/nasType"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/smf/pkg/service"
)

var testConfig = factory.Config{
	Info: &factory.Info{
		Version:     "1.0.0",
		Description: "SMF procdeure test configuration",
	},
	Configuration: &factory.Configuration{
		Sbi: &factory.Sbi{
			Scheme:       "http",
			RegisterIPv4: "127.0.0.1",
			BindingIPv4:  "127.0.0.1",
			Port:         8000,
		},
	},
}

var createData = &models.SmContextCreateData{
	Supi:         "imsi-208930000000001",
	Pei:          "imeisv-1110000000000000",
	Gpsi:         "msisdn-",
	PduSessionId: 10,
	Dnn:          "internet",
	SNssai: &models.Snssai{
		Sst: 1,
		Sd:  "112232",
	},
	ServingNfId: "c8d0ee65-f466-48aa-a42f-235ec771cb52",
	Guami: &models.Guami{
		PlmnId: &models.PlmnId{
			Mcc: "208",
			Mnc: "93",
		},
		AmfId: "cafe00",
	},
	AnType: "3GPP_ACCESS",
	ServingNetwork: &models.PlmnId{
		Mcc: "208",
		Mnc: "93",
	},
}

var sessSubData = []models.SessionManagementSubscriptionData{
	{
		SingleNssai: &models.Snssai{
			Sst: 1,
			Sd:  "112232",
		},
		DnnConfigurations: map[string]models.DnnConfiguration{
			"internet": {
				PduSessionTypes: &models.PduSessionTypes{
					DefaultSessionType: "IPV4",
					AllowedSessionTypes: []models.PduSessionType{
						"IPV4",
					},
				},
				SscModes: &models.SscModes{
					DefaultSscMode: "SSC_MODE_1",
					AllowedSscModes: []models.SscMode{
						"SSC_MODE_1",
						"SSC_MODE_2",
						"SSC_MODE_3",
					},
				},
				Var5gQosProfile: &models.SubscribedDefaultQos{
					Var5qi: 9,
					Arp: &models.Arp{
						PriorityLevel: 8,
					},
					PriorityLevel: 8,
				},
				SessionAmbr: &models.Ambr{
					Uplink:   "1000 Kbps",
					Downlink: "1000 Kbps",
				},
			},
		},
	},
}

func TestSendSMPolicyAssociationUpdateByUERequestModification(t *testing.T) {
	smf_context.InitSmfContext(&testConfig)

	testCases := []struct {
		name         string
		smContext    *smf_context.SMContext
		qosRules     nasType.QoSRules
		qosFlowDescs nasType.QoSFlowDescs

		smPolicyDecision *models.SmPolicyDecision
		responseErr      error
	}{
		{
			name:             "QoSRules is nil",
			smContext:        smf_context.NewSMContext(createData, sessSubData),
			qosRules:         nasType.QoSRules{},
			qosFlowDescs:     nasType.QoSFlowDescs{nasType.QoSFlowDesc{}},
			smPolicyDecision: nil,
			responseErr:      fmt.Errorf("QoS Rule not found"),
		},
		{
			name:             "QoSFlowDescs is nil",
			smContext:        smf_context.NewSMContext(createData, sessSubData),
			qosRules:         nasType.QoSRules{nasType.QoSRule{}},
			qosFlowDescs:     nasType.QoSFlowDescs{},
			smPolicyDecision: nil,
			responseErr:      fmt.Errorf("QoS Flow Description not found"),
		},
	}

	mockSmf := service.NewMockSmfAppInterface(gomock.NewController(t))
	consumer, errNewConsumer := consumer.NewConsumer(mockSmf)
	if errNewConsumer != nil {
		t.Fatalf("Failed to create consumer: %+v", errNewConsumer)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			smPolicyDecision, err := consumer.SendSMPolicyAssociationUpdateByUERequestModification(
				tc.smContext, tc.qosRules, tc.qosFlowDescs)

			require.Equal(t, tc.smPolicyDecision, smPolicyDecision)
			require.Equal(t, tc.responseErr.Error(), err.Error())
		})
	}
}
