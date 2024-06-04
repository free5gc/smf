package message_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/pfcp/pfcpType"
)

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
	
}

func TestBuildPfcpSessionEstablishmentResponse(t *testing.T) {
	
}

func TestBuildPfcpSessionModificationRequest(t *testing.T) {

}

func TestBuildPfcpSessionModificationResponse(t *testing.T) {

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
