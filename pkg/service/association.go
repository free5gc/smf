package service

import (
	"fmt"

	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/message"
)

func setupPfcpAssociation(upf *smf_context.UPF, upfStr string) error {
	logger.AppLog.Infof("Sending PFCP Association Request to UPF%s", upfStr)

	resMsg, err := message.SendPfcpAssociationSetupRequest(upf.NodeID)
	if err != nil {
		return err
	}

	rsp := resMsg.PfcpMessage.Body.(pfcp.PFCPAssociationSetupResponse)

	if rsp.Cause == nil || rsp.Cause.CauseValue != pfcpType.CauseRequestAccepted {
		return fmt.Errorf("received PFCP Association Setup Not Accepted Response from UPF%s", upfStr)
	}

	nodeID := rsp.NodeID
	if nodeID == nil {
		return fmt.Errorf("pfcp association needs NodeID")
	}

	logger.AppLog.Infof("Received PFCP Association Setup Accepted Response from UPF%s", upfStr)

	upf.UPFStatus = smf_context.AssociatedSetUpSuccess

	if rsp.UserPlaneIPResourceInformation != nil {
		upf.UPIPInfo = *rsp.UserPlaneIPResourceInformation

		logger.AppLog.Infof("UPF(%s)[%s] setup association",
			upf.NodeID.ResolveNodeIdToIp().String(), upf.UPIPInfo.NetworkInstance)
	}

	return nil
}
