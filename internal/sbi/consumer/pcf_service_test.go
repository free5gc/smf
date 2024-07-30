package consumer_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/free5gc/nas/nasType"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/context"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/smf/pkg/service"
)

var userPlaneConfig = factory.UserPlaneInformation{
	UPNodes: map[string]*factory.UPNode{
		"GNodeB": {
			Type: "AN",
		},
		"UPF1": {
			Type:   "UPF",
			NodeID: "10.4.0.11",
			Addr:   "10.4.0.11",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 1,
						Sd:  "010203",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn:      "internet",
							DnaiList: []string{"mec"},
						},
					},
				},
			},
			InterfaceUpfInfoList: []*factory.InterfaceUpfInfoItem{
				{
					InterfaceType: "N3",
					Endpoints: []string{
						"10.3.0.11",
					},
					NetworkInstances: []string{"internet"},
				},
				{
					InterfaceType: "N9",
					Endpoints: []string{
						"10.3.0.11",
					},
					NetworkInstances: []string{"internet"},
				},
			},
		},
		"UPF2": {
			Type:   "UPF",
			NodeID: "10.4.0.12",
			Addr:   "10.4.0.12",
			SNssaiInfos: []*factory.SnssaiUpfInfoItem{
				{
					SNssai: &models.Snssai{
						Sst: 1,
						Sd:  "010203",
					},
					DnnUpfInfoList: []*factory.DnnUpfInfoItem{
						{
							Dnn: "internet",
							Pools: []*factory.UEIPPool{
								{Cidr: "10.60.0.0/16"},
							},
						},
					},
				},
			},
			InterfaceUpfInfoList: []*factory.InterfaceUpfInfoItem{
				{
					InterfaceType: "N9",
					Endpoints: []string{
						"10.3.0.12",
					},
					NetworkInstances: []string{"internet"},
				},
			},
		},
	},
	Links: []*factory.UPLink{
		{
			A: "GNodeB",
			B: "UPF1",
		},
		{
			A: "UPF1",
			B: "UPF2",
		},
	},
}

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
		UserPlaneInformation: userPlaneConfig,
	},
}

func TestSendSMPolicyAssociationUpdateByUERequestModification(t *testing.T) {
	context.InitSmfContext(&testConfig)

	testCases := []struct {
		name         string
		smContext    *smf_context.SMContext
		qosRules     nasType.QoSRules
		qosFlowDescs nasType.QoSFlowDescs

		smPolicyDecision *models.SmPolicyDecision
		responseErr      error
	}{
		{
			name:             "QosRules is nil",
			smContext:        smf_context.NewSMContext("imsi-208930000000001", 10),
			qosRules:         nasType.QoSRules{},
			qosFlowDescs:     nasType.QoSFlowDescs{nasType.QoSFlowDesc{}},
			smPolicyDecision: nil,
			responseErr:      fmt.Errorf("QoS Rule not found"),
		},
		{
			name:             "QosRules is nil",
			smContext:        smf_context.NewSMContext("imsi-208930000000001", 10),
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
