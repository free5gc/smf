package service

import (
	"context"
	"fmt"
	"time"

	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/message"
)

func toBeAssociatedWithUPF(ctx context.Context, upf *smf_context.UPF) {
	var upfStr string
	if upf.NodeID.NodeIdType == pfcpType.NodeIdTypeFqdn {
		upfStr = fmt.Sprintf("[%s](%s)", upf.NodeID.FQDN, upf.NodeID.ResolveNodeIdToIp().String())
	} else {
		upfStr = fmt.Sprintf("[%s]", upf.NodeID.ResolveNodeIdToIp().String())
	}
	ensureSetupPfcpAssociation(ctx, upf, upfStr)
}

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func ensureSetupPfcpAssociation(ctx context.Context, upf *smf_context.UPF, upfStr string) {
	var alertTime time.Time
	for {
		alertInterval := smf_context.SMF_Self().AssociationSetupFailedAlertInterval
		err := setupPfcpAssociation(upf, upfStr)
		if err == nil {
			return
		}
		now := time.Now()
		if alertTime.IsZero() || now.After(alertTime.Add(alertInterval)) {
			logger.AppLog.Errorf("Failed to setup an association with UPF%s, error:%+v", upfStr, err)
			alertTime = now
		}

		if isDone(ctx) {
			logger.AppLog.Infof("Canceled association request to UPF%s", upfStr)
			return
		}
	}
}

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
