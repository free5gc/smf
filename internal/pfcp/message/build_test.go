package message_test

import (
	"net"
	"testing"

	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/stretchr/testify/assert"
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

var testNodeID = pfcpType.NodeID{
	NodeIdType: 0,
	IP:         net.ParseIP("10.4.0.1").To4(),
}

func initSmfContext() {
	context.InitSmfContext(&testConfig)
}

func initRuleList() ([]*context.PDR, []*context.FAR, []*context.BAR,
	[]*context.QER, []*context.URR,
) {
	var testPDR = &context.PDR{
		PDRID: uint16(1),
		State: context.RULE_INITIAL,
		OuterHeaderRemoval: &pfcpType.OuterHeaderRemoval{
			OuterHeaderRemovalDescription: (1),
		},
		FAR: &context.FAR{},
		URR: []*context.URR{},
		QER: []*context.QER{},
	}

	var testFAR = &context.FAR{
		FARID: uint32(123),
		// State Can be RULE_INITIAL or RULE_UPDATE or RULE_REMOVE
		State: context.RULE_INITIAL,
		ApplyAction: pfcpType.ApplyAction{
			Forw: true,
		},
		ForwardingParameters: &context.ForwardingParameters{},
		BAR:                  &context.BAR{},
	}

	var testBAR = &context.BAR{
		BARID: uint8(124),
		// State Can be RULE_INITIAL or RULE_UPDATE or RULE_REMOVE
		State: context.RULE_INITIAL,
	}

	var testQER = &context.QER{
		QERID: uint32(123),
		// State Can be RULE_INITIAL or RULE_UPDATE or RULE_REMOVE
		State: context.RULE_INITIAL,
	}

	var testURR = &context.URR{
		URRID: uint32(123),
		// State Can be RULE_INITIAL or RULE_UPDATE or RULE_REMOVE
		State: context.RULE_INITIAL,
	}
	pdrList := make([]*context.PDR, 0)
	farList := make([]*context.FAR, 0)
	barList := make([]*context.BAR, 0)
	qerList := make([]*context.QER, 0)
	urrList := make([]*context.URR, 0)
	pdrList = append(pdrList, testPDR)
	farList = append(farList, testFAR)
	barList = append(barList, testBAR)
	qerList = append(qerList, testQER)
	urrList = append(urrList, testURR)
	return pdrList, farList, barList, qerList, urrList
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
	initSmfContext()
	var smctx = context.NewSMContext("imsi-208930000000001", 10)
	pdrList, farList, barList, qerList, urrList := initRuleList()
	smctx.PFCPContext["10.4.0.1"] = &context.PFCPSessionContext{}

	req, err := message.BuildPfcpSessionEstablishmentRequest(
		testNodeID, "10.4.0.1", smctx, pdrList, farList, barList, qerList, urrList)
	if err != nil {
		t.Errorf("TestBuildPfcpSessionEstablishmentRequest failed: %v", err)
	}
	assert.Equal(t, uint8(0), req.NodeID.NodeIdType)
	assert.NotNil(t, req.CPFSEID)
	assert.NotNil(t, req.PDNType)
	assert.NotNil(t, req.CreatePDR)
	assert.NotNil(t, req.CreateFAR)
	assert.NotNil(t, req.CreateBAR)
	assert.NotNil(t, req.CreateQER)
	assert.NotNil(t, req.CreateURR)
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
	initSmfContext()
	var smctx = context.NewSMContext("imsi-208930000000001", 10)
	pdrList, farList, barList, qerList, urrList := initRuleList()
	smctx.PFCPContext["10.4.0.1"] = &context.PFCPSessionContext{}

	req, err := message.BuildPfcpSessionModificationRequest(
		testNodeID, "10.4.0.1", smctx, pdrList, farList, barList, qerList, urrList)
	if err != nil {
		t.Errorf("TestBuildPfcpSessionModificationRequest failed: %v", err)
	}

	assert.Equal(t, context.RULE_CREATE, pdrList[0].State)

	assert.NotNil(t, req.CPFSEID)
	assert.NotNil(t, req.CreateBAR)
	assert.NotNil(t, req.CreateFAR)
	assert.NotNil(t, req.CreateBAR)
	assert.NotNil(t, req.CreateQER)
	assert.NotNil(t, req.CreateURR)
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
