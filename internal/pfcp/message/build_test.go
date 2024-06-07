package message_test

import (
	"net"
	"testing"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/stretchr/testify/assert"
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

func initConfig() {
	context.InitSmfContext(&testConfig)
	factory.SmfConfig = &testConfig
}

func TestBuildPfcpAssociationSetupRequest(t *testing.T) {
	emptyReq, err := message.BuildPfcpAssociationSetupRequest()
	if err != nil {
		t.Errorf("TestBuildPfcpAssociationSetupRequest failed: %v", err)
	}

	// BuildPfcpAssociationSetupRequest buila a empty template of pfcp.PFCPAssociationSetupRequest
	assert.Equal(t, uint8(0), emptyReq.NodeID.NodeIdType)
	assert.Equal(t, net.IP(nil), emptyReq.NodeID.IP)
	assert.Equal(t, "", emptyReq.NodeID.FQDN)

	assert.Equal(t,
		udp.ServerStartTime,
		emptyReq.RecoveryTimeStamp.RecoveryTimeStamp)
	assert.Nil(t,
		emptyReq.UPFunctionFeatures)
	assert.Equal(t,
		pfcpType.CPFunctionFeatures{SupportedFeatures: 0},
		*emptyReq.CPFunctionFeatures)
}

func TestBuildPfcpAssociationSetupResponse(t *testing.T) {
	cause := pfcpType.Cause{CauseValue: pfcpType.CauseRequestAccepted}
	rsp, err := message.BuildPfcpAssociationSetupResponse(cause)

	if err != nil {
		t.Errorf("TestBuildPfcpAssociationSetupResponse failed: %v", err)
	}

	assert.Equal(t, uint8(0), rsp.NodeID.NodeIdType)
	assert.Equal(t, cause, *rsp.Cause)

	assert.Nil(t,
		rsp.UPFunctionFeatures)
	assert.Equal(t,
		pfcpType.CPFunctionFeatures{SupportedFeatures: 0},
		*rsp.CPFunctionFeatures)
}

func TestBuildPfcpAssociationReleaseRequest(t *testing.T) {
	emptyReq, err := message.BuildPfcpAssociationReleaseRequest()
	if err != nil {
		t.Errorf("TestBuildPfcpAssociationReleaseRequest failed: %v", err)
	}

	assert.Equal(t, uint8(0), emptyReq.NodeID.NodeIdType)
}

func TestBuildPfcpAssociationReleaseResponse(t *testing.T) {
	cause := pfcpType.Cause{CauseValue: pfcpType.CauseRequestAccepted}
	rsp, err := message.BuildPfcpAssociationReleaseResponse(cause)

	if err != nil {
		t.Errorf("TestBuildPfcpAssociationReleaseResponse failed: %v", err)
	}

	assert.Equal(t, uint8(0), rsp.NodeID.NodeIdType)
	assert.Equal(t, cause, *rsp.Cause)
}

func TestBuildPfcpSessionEstablishmentRequest(t *testing.T) {
	initConfig()
	var NodeID = pfcpType.NodeID{
		NodeIdType: 0,
		IP:         net.ParseIP("10.4.0.11").To4(),
	}
	smctx := context.NewSMContext("imsi-208930000000001", 10)
	pdrList := make([]*context.PDR, 0, 2)
	farList := make([]*context.FAR, 0, 2)
	qerList := make([]*context.QER, 0, 2)
	urrList := make([]*context.URR, 0, 2)
	barList := make([]*context.BAR, 0, 2)

	smctx.PFCPContext["10.4.0.11"] = &context.PFCPSessionContext{}

	req, err := message.BuildPfcpSessionEstablishmentRequest(NodeID, "10.4.0.11", smctx, pdrList, farList, barList, qerList, urrList)
	if err != nil {
		t.Errorf("TestBuildPfcpSessionEstablishmentRequest failed: %v", err)
	}

	createPDR := make([]*pfcp.CreatePDR, 0)
	assert.Equal(t, uint8(0), req.NodeID.NodeIdType)
	assert.NotNil(t, req.CPFSEID)
	assert.NotNil(t, req.PDNType)
	assert.Equal(t, createPDR, req.CreatePDR)
}

// hsien
func TestBuildPfcpSessionEstablishmentResponse(t *testing.T) {
	rsp, err := message.BuildPfcpSessionEstablishmentResponse()
	if err != nil {
		t.Errorf("TestBuildPfcpSessionEstablishmentResponse failed: %v", err)
	}
	assert.Equal(t, uint8(0), rsp.NodeID.NodeIdType)
	assert.Equal(t, pfcpType.CauseRequestAccepted, rsp.Cause.CauseValue)
	assert.NotNil(t, rsp.UPFSEID)
	assert.NotNil(t, rsp.CreatedPDR)
}

func TestBuildPfcpSessionModificationRequest(t *testing.T) {

}

func TestBuildPfcpSessionModificationResponse(t *testing.T) {
	rsp, err := message.BuildPfcpSessionEstablishmentResponse()
	if err != nil {
		t.Errorf("BuildPfcpSessionModificationResponse failed: %v", err)
	}
	assert.Equal(t, pfcpType.CauseRequestAccepted, rsp.Cause.CauseValue)
	assert.NotNil(t, rsp.OffendingIE)
	assert.NotNil(t, rsp.CreatedPDR)
}

func TestBuildPfcpSessionDeletionResponse(t *testing.T) {
	_, err := message.BuildPfcpSessionDeletionResponse()
	if err != nil {
		t.Errorf("TestBuildPfcpSessionDeletionResponse failed: %v", err)
	}
}

func TestBuildPfcpSessionReportResponse(t *testing.T) {
	cause := pfcpType.Cause{CauseValue: pfcpType.CauseRequestAccepted}
	rsp, err := message.BuildPfcpSessionReportResponse(cause)
	if err != nil {
		t.Errorf("TestBuildPfcpSessionReportResponse failed: %v", err)
	}
	assert.Equal(t, cause, *rsp.Cause)
}

func TestBuildPfcpHeartbeatRequest(t *testing.T) {
	rsq, err := message.BuildPfcpHeartbeatRequest()
	if err != nil {
		t.Errorf("TestBuildPfcpHeartbeatRequest failed: %v", err)
	}

	assert.Equal(t, udp.ServerStartTime, rsq.RecoveryTimeStamp.RecoveryTimeStamp)
}
