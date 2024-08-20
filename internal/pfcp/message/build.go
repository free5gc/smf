package message

import (
	"net"
	"time"

	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/udp"
)

func BuildPfcpAssociationSetupRequest() (pfcp.PFCPAssociationSetupRequest, error) {
	msg := pfcp.PFCPAssociationSetupRequest{}

	msg.NodeID = &smf_context.GetSelf().CPNodeID

	msg.RecoveryTimeStamp = &pfcpType.RecoveryTimeStamp{
		RecoveryTimeStamp: udp.ServerStartTime,
	}

	msg.CPFunctionFeatures = &pfcpType.CPFunctionFeatures{
		SupportedFeatures: 0,
	}

	return msg, nil
}

func BuildPfcpAssociationSetupResponse(cause pfcpType.Cause) (pfcp.PFCPAssociationSetupResponse, error) {
	msg := pfcp.PFCPAssociationSetupResponse{}

	msg.NodeID = &smf_context.GetSelf().CPNodeID

	msg.Cause = &cause

	msg.RecoveryTimeStamp = &pfcpType.RecoveryTimeStamp{
		RecoveryTimeStamp: udp.ServerStartTime,
	}

	msg.CPFunctionFeatures = &pfcpType.CPFunctionFeatures{
		SupportedFeatures: 0,
	}

	return msg, nil
}

func BuildPfcpAssociationReleaseRequest() (pfcp.PFCPAssociationReleaseRequest, error) {
	msg := pfcp.PFCPAssociationReleaseRequest{}

	msg.NodeID = &smf_context.GetSelf().CPNodeID

	return msg, nil
}

func BuildPfcpAssociationReleaseResponse(cause pfcpType.Cause) (pfcp.PFCPAssociationReleaseResponse, error) {
	msg := pfcp.PFCPAssociationReleaseResponse{}

	msg.NodeID = &smf_context.GetSelf().CPNodeID

	msg.Cause = &cause

	return msg, nil
}

func pdrToCreatePDR(pdr *smf_context.PDR) *pfcp.CreatePDR {
	createPDR := &pfcp.CreatePDR{
		PDRID: &pfcpType.PacketDetectionRuleID{
			RuleId: pdr.PDRID,
		},
		Precedence: &pfcpType.Precedence{
			PrecedenceValue: pdr.Precedence,
		},
		PDI: &pfcp.PDI{
			SourceInterface: &pdr.PDI.SourceInterface,
			LocalFTEID:      pdr.PDI.LocalFTeid,
			NetworkInstance: pdr.PDI.NetworkInstance,
			UEIPAddress:     pdr.PDI.UEIPAddress,
		},
		OuterHeaderRemoval: pdr.OuterHeaderRemoval,
		FARID: &pfcpType.FARID{
			FarIdValue: pdr.FAR.FARID,
		},
	}

	if pdr.PDI.ApplicationID != "" {
		createPDR.PDI.ApplicationID = &pfcpType.ApplicationID{
			ApplicationIdentifier: []byte(pdr.PDI.ApplicationID),
		}
	}

	if pdr.PDI.SDFFilter != nil {
		createPDR.PDI.SDFFilter = pdr.PDI.SDFFilter
	}

	for _, qer := range pdr.QER {
		if qer != nil {
			createPDR.QERID = append(createPDR.QERID, &pfcpType.QERID{
				QERID: qer.QERID,
			})
		}
	}

	for _, urr := range pdr.URR {
		if urr != nil {
			createPDR.URRID = append(createPDR.URRID, &pfcpType.URRID{
				UrrIdValue: urr.URRID,
			})
		}
	}

	return createPDR
}

func farToCreateFAR(far *smf_context.FAR) *pfcp.CreateFAR {
	createFAR := new(pfcp.CreateFAR)

	createFAR.FARID = new(pfcpType.FARID)
	createFAR.FARID.FarIdValue = far.FARID

	createFAR.ApplyAction = new(pfcpType.ApplyAction)
	if far.ForwardingParameters != nil {
		createFAR.ApplyAction.Forw = true
	} else {
		/*
			29.244 v15.3 Table 7.5.2.3-1
			Farwarding Parameters IE shall be present when the Apply-Action requests the packets to be forwarded.
		*/
		// FAR without Farwarding Parameters set Apply Action as Drop instead of Forward.
		createFAR.ApplyAction.Forw = false
		createFAR.ApplyAction.Drop = true
	}

	if far.BAR != nil {
		createFAR.BARID = new(pfcpType.BARID)
		createFAR.BARID.BarIdValue = far.BAR.BARID
	}

	if far.ForwardingParameters != nil {
		createFAR.ForwardingParameters = &pfcp.ForwardingParametersIEInFAR{
			DestinationInterface: &far.ForwardingParameters.DestinationInterface,
			NetworkInstance:      far.ForwardingParameters.NetworkInstance,
			OuterHeaderCreation:  far.ForwardingParameters.OuterHeaderCreation,
		}
		if far.ForwardingParameters.ForwardingPolicyID != "" {
			createFAR.ForwardingParameters.ForwardingPolicy = &pfcpType.ForwardingPolicy{
				ForwardingPolicyIdentifierLength: uint8(len(far.ForwardingParameters.ForwardingPolicyID)),
				ForwardingPolicyIdentifier:       []byte(far.ForwardingParameters.ForwardingPolicyID),
			}
		}
	}

	return createFAR
}

func barToCreateBAR(bar *smf_context.BAR) *pfcp.CreateBAR {
	createBAR := new(pfcp.CreateBAR)

	createBAR.BARID = new(pfcpType.BARID)
	createBAR.BARID.BarIdValue = bar.BARID

	createBAR.DownlinkDataNotificationDelay = new(pfcpType.DownlinkDataNotificationDelay)

	// createBAR.SuggestedBufferingPacketsCount = new(pfcpType.SuggestedBufferingPacketsCount)

	return createBAR
}

func qerToCreateQER(qer *smf_context.QER) *pfcp.CreateQER {
	createQER := new(pfcp.CreateQER)

	createQER.QERID = new(pfcpType.QERID)
	createQER.QERID.QERID = qer.QERID
	createQER.GateStatus = qer.GateStatus

	createQER.QoSFlowIdentifier = &qer.QFI
	createQER.MaximumBitrate = qer.MBR
	createQER.GuaranteedBitrate = qer.GBR

	return createQER
}

func urrToCreateURR(urr *smf_context.URR) *pfcp.CreateURR {
	createURR := new(pfcp.CreateURR)

	createURR.URRID = &pfcpType.URRID{
		UrrIdValue: urr.URRID,
	}
	createURR.MeasurementMethod = &pfcpType.MeasurementMethod{}
	switch urr.MeasureMethod {
	case smf_context.MesureMethodVol:
		createURR.MeasurementMethod.Volum = true
	case smf_context.MesureMethodTime:
		createURR.MeasurementMethod.Durat = true
	}
	createURR.ReportingTriggers = &urr.ReportingTrigger
	if urr.MeasurementPeriod != 0 {
		createURR.MeasurementPeriod = &pfcpType.MeasurementPeriod{
			MeasurementPeriod: uint32(urr.MeasurementPeriod / time.Second),
		}
	}
	if !urr.QuotaValidityTime.IsZero() {
		createURR.QuotaValidityTime = &pfcpType.QuotaValidityTime{
			QuotaValidityTime: uint32(urr.QuotaValidityTime.Sub(
				time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)) / 1000000000),
		}
	}

	if urr.VolumeThreshold != 0 {
		createURR.VolumeThreshold = &pfcpType.VolumeThreshold{
			Dlvol:          true,
			Ulvol:          true,
			DownlinkVolume: urr.VolumeThreshold,
			UplinkVolume:   urr.VolumeThreshold,
		}
	}
	if urr.VolumeQuota != 0 {
		createURR.VolumeQuota = &pfcpType.VolumeQuota{
			Tovol:          true,
			Dlvol:          true,
			Ulvol:          true,
			TotalVolume:    urr.VolumeQuota,
			DownlinkVolume: urr.VolumeQuota,
			UplinkVolume:   urr.VolumeQuota,
		}
	}

	createURR.MeasurementInformation = &urr.MeasurementInformation

	return createURR
}

func pdrToUpdatePDR(pdr *smf_context.PDR) *pfcp.UpdatePDR {
	updatePDR := new(pfcp.UpdatePDR)

	updatePDR.PDRID = new(pfcpType.PacketDetectionRuleID)
	updatePDR.PDRID.RuleId = pdr.PDRID

	updatePDR.Precedence = new(pfcpType.Precedence)
	updatePDR.Precedence.PrecedenceValue = pdr.Precedence

	updatePDR.PDI = &pfcp.PDI{
		SourceInterface: &pdr.PDI.SourceInterface,
		LocalFTEID:      pdr.PDI.LocalFTeid,
		NetworkInstance: pdr.PDI.NetworkInstance,
		UEIPAddress:     pdr.PDI.UEIPAddress,
	}

	if pdr.PDI.ApplicationID != "" {
		updatePDR.PDI.ApplicationID = &pfcpType.ApplicationID{
			ApplicationIdentifier: []byte(pdr.PDI.ApplicationID),
		}
	}

	if pdr.PDI.SDFFilter != nil {
		updatePDR.PDI.SDFFilter = pdr.PDI.SDFFilter
	}

	updatePDR.OuterHeaderRemoval = pdr.OuterHeaderRemoval

	updatePDR.FARID = &pfcpType.FARID{
		FarIdValue: pdr.FAR.FARID,
	}

	updatePDR.FARID = &pfcpType.FARID{
		FarIdValue: pdr.FAR.FARID,
	}

	for _, urr := range pdr.URR {
		if urr != nil {
			updatePDR.URRID = append(updatePDR.URRID, &pfcpType.URRID{
				UrrIdValue: urr.URRID,
			})
		}
	}

	return updatePDR
}

func farToUpdateFAR(far *smf_context.FAR) *pfcp.UpdateFAR {
	updateFAR := new(pfcp.UpdateFAR)

	updateFAR.FARID = new(pfcpType.FARID)
	updateFAR.FARID.FarIdValue = far.FARID

	if far.BAR != nil {
		updateFAR.BARID = new(pfcpType.BARID)
		updateFAR.BARID.BarIdValue = far.BAR.BARID
	}

	updateFAR.ApplyAction = new(pfcpType.ApplyAction)
	updateFAR.ApplyAction.Forw = far.ApplyAction.Forw
	updateFAR.ApplyAction.Buff = far.ApplyAction.Buff
	updateFAR.ApplyAction.Nocp = far.ApplyAction.Nocp
	updateFAR.ApplyAction.Dupl = far.ApplyAction.Dupl
	updateFAR.ApplyAction.Drop = far.ApplyAction.Drop

	if far.ForwardingParameters != nil {
		updateFAR.UpdateForwardingParameters = &pfcp.UpdateForwardingParametersIEInFAR{
			DestinationInterface: &far.ForwardingParameters.DestinationInterface,
			NetworkInstance:      far.ForwardingParameters.NetworkInstance,
			OuterHeaderCreation:  far.ForwardingParameters.OuterHeaderCreation,
			PFCPSMReqFlags: &pfcpType.PFCPSMReqFlags{
				Sndem: far.ForwardingParameters.SendEndMarker,
			},
		}
		if far.ForwardingParameters.ForwardingPolicyID != "" {
			updateFAR.UpdateForwardingParameters.ForwardingPolicy = &pfcpType.ForwardingPolicy{
				ForwardingPolicyIdentifierLength: uint8(len(far.ForwardingParameters.ForwardingPolicyID)),
				ForwardingPolicyIdentifier:       []byte(far.ForwardingParameters.ForwardingPolicyID),
			}
		}
	}

	return updateFAR
}

func urrToUpdateURR(urr *smf_context.URR) *pfcp.UpdateURR {
	updateURR := new(pfcp.UpdateURR)

	updateURR.URRID = &pfcpType.URRID{
		UrrIdValue: urr.URRID,
	}
	updateURR.MeasurementMethod = &pfcpType.MeasurementMethod{}
	switch urr.MeasureMethod {
	case smf_context.MesureMethodVol:
		updateURR.MeasurementMethod.Volum = true
	case smf_context.MesureMethodTime:
		updateURR.MeasurementMethod.Durat = true
	}
	updateURR.ReportingTriggers = &urr.ReportingTrigger
	if urr.MeasurementPeriod != 0 {
		updateURR.MeasurementPeriod = &pfcpType.MeasurementPeriod{
			MeasurementPeriod: uint32(urr.MeasurementPeriod / time.Second),
		}
	}
	if urr.QuotaValidityTime.IsZero() {
		updateURR.QuotaValidityTime = &pfcpType.QuotaValidityTime{
			QuotaValidityTime: uint32(urr.QuotaValidityTime.Sub(
				time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)) / 1000000000),
		}
	}

	if urr.VolumeThreshold != 0 {
		updateURR.VolumeThreshold = &pfcpType.VolumeThreshold{
			Dlvol:          true,
			Ulvol:          true,
			Tovol:          true,
			TotalVolume:    urr.VolumeThreshold,
			DownlinkVolume: urr.VolumeThreshold,
			UplinkVolume:   urr.VolumeThreshold,
		}
	}
	if urr.VolumeQuota != 0 {
		updateURR.VolumeQuota = &pfcpType.VolumeQuota{
			Tovol:          true,
			Dlvol:          true,
			Ulvol:          true,
			TotalVolume:    urr.VolumeQuota,
			DownlinkVolume: urr.VolumeQuota,
			UplinkVolume:   urr.VolumeQuota,
		}
	}
	updateURR.MeasurementInformation = &urr.MeasurementInformation

	return updateURR
}

func BuildPfcpSessionEstablishmentRequest(
	pfcpContext *smf_context.PFCPSessionContext,
) (pfcp.PFCPSessionEstablishmentRequest, error) {
	localSEID := pfcpContext.LocalSEID

	msg := pfcp.PFCPSessionEstablishmentRequest{
		NodeID: &smf_context.GetSelf().CPNodeID,
		CPFSEID: &pfcpType.FSEID{
			V4:          smf_context.GetSelf().ExternalIP().To4() != nil,
			V6:          smf_context.GetSelf().ExternalIP().To4() == nil,
			Seid:        localSEID,
			Ipv4Address: smf_context.GetSelf().ExternalIP().To4(),
		},
		PDNType: &pfcpType.PDNType{
			PdnType: pfcpType.PDNTypeIpv4,
		},
		CreatePDR: make([]*pfcp.CreatePDR, 0, 2),
		CreateFAR: make([]*pfcp.CreateFAR, 0, 2),
	}

	// QER and URR may appear in multiple PDRs, so we need to process them only once
	qerMap := make(map[uint32]*smf_context.QER)
	urrMap := make(map[uint32]*smf_context.URR)

	for _, pdr := range pfcpContext.PDRs {
		if pdr.CheckState(smf_context.RULE_INITIAL) {
			msg.CreatePDR = append(msg.CreatePDR, pdrToCreatePDR(pdr))
			pdr.SetState(smf_context.RULE_CREATE)
		}

		if far := pdr.FAR; far != nil {
			if far.CheckState(smf_context.RULE_INITIAL) {
				msg.CreateFAR = append(msg.CreateFAR, farToCreateFAR(far))
				far.SetState(smf_context.RULE_CREATE)
			}

			if bar := far.BAR; bar != nil {
				if bar.CheckState(smf_context.RULE_INITIAL) {
					msg.CreateBAR = append(msg.CreateBAR, barToCreateBAR(bar))
					bar.SetState(smf_context.RULE_CREATE)
				}
			}
		}

		for _, qer := range pdr.QER {
			if _, exists := qerMap[qer.QERID]; !exists {
				qerMap[qer.QERID] = qer
				if qer.CheckState(smf_context.RULE_INITIAL) {
					msg.CreateQER = append(msg.CreateQER, qerToCreateQER(qer))
					qer.SetState(smf_context.RULE_CREATE)
				}
			}
		}

		for _, urr := range pdr.URR {
			if _, exists := urrMap[urr.URRID]; !exists {
				urrMap[urr.URRID] = urr
				if urr.CheckState(smf_context.RULE_INITIAL) {
					msg.CreateURR = append(msg.CreateURR, urrToCreateURR(urr))
					urr.SetState(smf_context.RULE_CREATE)
				}
			}
		}
	}

	return msg, nil
}

func BuildPfcpSessionEstablishmentResponse() (pfcp.PFCPSessionEstablishmentResponse, error) {
	msg := pfcp.PFCPSessionEstablishmentResponse{
		NodeID: &smf_context.GetSelf().CPNodeID,
		Cause: &pfcpType.Cause{
			CauseValue: pfcpType.CauseRequestAccepted,
		},
		OffendingIE: &pfcpType.OffendingIE{
			TypeOfOffendingIe: 12345,
		},
		UPFSEID: &pfcpType.FSEID{
			V4:          true,
			V6:          false, //;
			Seid:        123456789123456789,
			Ipv4Address: net.ParseIP("192.168.1.1").To4(),
		},
		CreatedPDR: &pfcp.CreatedPDR{
			PDRID: &pfcpType.PacketDetectionRuleID{
				RuleId: 256,
			},
			LocalFTEID: &pfcpType.FTEID{
				Chid:        false,
				Ch:          false,
				V6:          false,
				V4:          true,
				Teid:        12345,
				Ipv4Address: net.ParseIP("192.168.1.1").To4(),
			},
		},
	}

	return msg, nil
}

// TODO: Replace dummy value in PFCP message
func BuildPfcpSessionModificationRequest(
	pfcpContext *smf_context.PFCPSessionContext,
) (pfcp.PFCPSessionModificationRequest, error) {
	localSEID := pfcpContext.LocalSEID

	msg := pfcp.PFCPSessionModificationRequest{
		CPFSEID: &pfcpType.FSEID{
			V4:          smf_context.GetSelf().ExternalIP().To4() != nil,
			V6:          smf_context.GetSelf().ExternalIP().To4() == nil,
			Seid:        localSEID,
			Ipv4Address: smf_context.GetSelf().ExternalIP().To4(),
		},
		UpdatePDR: make([]*pfcp.UpdatePDR, 0, 2),
		UpdateFAR: make([]*pfcp.UpdateFAR, 0, 2),
		UpdateURR: make([]*pfcp.UpdateURR, 0, 12),
	}

	for _, pdr := range pfcpContext.PDRs {
		switch pdr.GetState() {
		case smf_context.RULE_INITIAL:
			msg.CreatePDR = append(msg.CreatePDR, pdrToCreatePDR(pdr))
			pdr.SetState(smf_context.RULE_CREATE)
		case smf_context.RULE_UPDATE:
			msg.UpdatePDR = append(msg.UpdatePDR, pdrToUpdatePDR(pdr))
			pdr.SetState(smf_context.RULE_CREATE)
		case smf_context.RULE_REMOVE:
			msg.RemovePDR = append(msg.RemovePDR, &pfcp.RemovePDR{
				PDRID: &pfcpType.PacketDetectionRuleID{
					RuleId: pdr.PDRID,
				},
			})
			// do NOT set rule to CREATE
		case smf_context.RULE_SYNCED:
			// skip, already transferred to this UPF
		}

		if far := pdr.FAR; far != nil {
			switch far.GetState() {
			case smf_context.RULE_INITIAL:
				msg.CreateFAR = append(msg.CreateFAR, farToCreateFAR(far))
				far.SetState(smf_context.RULE_CREATE)
			case smf_context.RULE_UPDATE:
				msg.UpdateFAR = append(msg.UpdateFAR, farToUpdateFAR(far))
				far.SetState(smf_context.RULE_CREATE)
			case smf_context.RULE_REMOVE:
				msg.RemoveFAR = append(msg.RemoveFAR, &pfcp.RemoveFAR{
					FARID: &pfcpType.FARID{
						FarIdValue: far.FARID,
					},
				})
				// do NOT set rule to CREATE
			case smf_context.RULE_SYNCED:
				// skip, already transferred to this UPF
			}

			if bar := far.BAR; bar != nil {
				switch bar.GetState() {
				case smf_context.RULE_INITIAL:
					msg.CreateBAR = append(msg.CreateBAR, barToCreateBAR(bar))
					bar.SetState(smf_context.RULE_CREATE)
				case smf_context.RULE_SYNCED:
					// skip, already transferred to this UPF
				}
			}
		}

		for _, qer := range pdr.QER {
			switch qer.GetState() {
			case smf_context.RULE_INITIAL:
				msg.CreateQER = append(msg.CreateQER, qerToCreateQER(qer))
				qer.SetState(smf_context.RULE_CREATE)
			case smf_context.RULE_SYNCED:
				// skip, already transferred to this UPF
			}
		}

		for _, urr := range pdr.URR {
			switch urr.GetState() {
			case smf_context.RULE_INITIAL:
				msg.CreateURR = append(msg.CreateURR, urrToCreateURR(urr))
				urr.SetState(smf_context.RULE_CREATE)
			case smf_context.RULE_UPDATE:
				msg.UpdateURR = append(msg.UpdateURR, urrToUpdateURR(urr))
				urr.SetState(smf_context.RULE_CREATE)
			case smf_context.RULE_REMOVE:
				msg.RemoveURR = append(msg.RemoveURR, &pfcp.RemoveURR{
					URRID: &pfcpType.URRID{
						UrrIdValue: urr.URRID,
					},
				})
			case smf_context.RULE_QUERY:
				msg.QueryURR = append(msg.QueryURR, &pfcp.QueryURR{
					URRID: &pfcpType.URRID{
						UrrIdValue: urr.URRID,
					},
				})
			case smf_context.RULE_SYNCED:
				// skip, already transferred to this UPF
			}
		}
	}

	return msg, nil
}

func BuildPfcpSessionRecoveryRequest(
	pfcpContext *smf_context.PFCPSessionContext,
) (pfcp.PFCPSessionEstablishmentRequest, error) {
	localSEID := pfcpContext.LocalSEID

	// recovery is a session establishment request that creates all PDRs
	// except for the ones in REMOVE state
	msg := pfcp.PFCPSessionEstablishmentRequest{
		NodeID: &smf_context.GetSelf().CPNodeID,
		CPFSEID: &pfcpType.FSEID{
			V4:          smf_context.GetSelf().ExternalIP().To4() != nil,
			V6:          smf_context.GetSelf().ExternalIP().To4() == nil,
			Seid:        localSEID,
			Ipv4Address: smf_context.GetSelf().ExternalIP().To4(),
		},
		PDNType: &pfcpType.PDNType{
			PdnType: pfcpType.PDNTypeIpv4,
		},
	}

	// QER and URR may appear in multiple PDRs, so we need to process them only once
	qerMap := make(map[uint32]*smf_context.QER)
	urrMap := make(map[uint32]*smf_context.URR)

	for _, pdr := range pfcpContext.PDRs {
		if !pdr.CheckState(smf_context.RULE_REMOVE) {
			msg.CreatePDR = append(msg.CreatePDR, pdrToCreatePDR(pdr))
			pdr.SetState(smf_context.RULE_CREATE)
		} // PDR is marked for removal, do not restore it

		if far := pdr.FAR; far != nil {
			if !far.CheckState(smf_context.RULE_REMOVE) {
				msg.CreateFAR = append(msg.CreateFAR, farToCreateFAR(far))
				far.SetState(smf_context.RULE_CREATE)
			} // FAR is marked for removal, do not restore it

			if bar := far.BAR; bar != nil {
				if !bar.CheckState(smf_context.RULE_REMOVE) {
					msg.CreateBAR = append(msg.CreateBAR, barToCreateBAR(bar))
					bar.SetState(smf_context.RULE_CREATE)
				} // BAR is marked for removal, do not restore it
			}
		}

		for _, qer := range pdr.QER {
			if !qer.CheckState(smf_context.RULE_REMOVE) {
				if _, exists := qerMap[qer.QERID]; !exists {
					qerMap[qer.QERID] = qer
					msg.CreateQER = append(msg.CreateQER, qerToCreateQER(qer))
					qer.SetState(smf_context.RULE_CREATE)
				}
			} // QER is marked for removal, do not restore it
		}

		for _, urr := range pdr.URR {
			if !urr.CheckState(smf_context.RULE_REMOVE) {
				if _, exists := urrMap[urr.URRID]; !exists {
					urrMap[urr.URRID] = urr
					msg.CreateURR = append(msg.CreateURR, urrToCreateURR(urr))
					urr.SetState(smf_context.RULE_CREATE)
				}
			} // URR is marked for removal, do not restore it
		}
	}

	return msg, nil
}

// TODO: Replace dummy value in PFCP message
func BuildPfcpSessionModificationResponse() (pfcp.PFCPSessionModificationResponse, error) {
	msg := pfcp.PFCPSessionModificationResponse{}

	msg.Cause = &pfcpType.Cause{
		CauseValue: pfcpType.CauseRequestAccepted,
	}

	msg.OffendingIE = &pfcpType.OffendingIE{
		TypeOfOffendingIe: 12345,
	}

	msg.CreatedPDR = &pfcp.CreatedPDR{
		PDRID: &pfcpType.PacketDetectionRuleID{
			RuleId: 256,
		},
		LocalFTEID: &pfcpType.FTEID{
			Chid:        false,
			Ch:          false,
			V6:          false,
			V4:          true,
			Teid:        12345,
			Ipv4Address: net.ParseIP("192.168.1.1").To4(),
		},
	}

	return msg, nil
}

func BuildPfcpSessionDeletionRequest() (pfcp.PFCPSessionDeletionRequest, error) {
	msg := pfcp.PFCPSessionDeletionRequest{}

	return msg, nil
}

// TODO: Replace dummy value in PFCP message
func BuildPfcpSessionDeletionResponse() (pfcp.PFCPSessionDeletionResponse, error) {
	msg := pfcp.PFCPSessionDeletionResponse{}

	msg.Cause = &pfcpType.Cause{
		CauseValue: pfcpType.CauseRequestAccepted,
	}

	msg.OffendingIE = &pfcpType.OffendingIE{
		TypeOfOffendingIe: 12345,
	}

	return msg, nil
}

func BuildPfcpSessionReportResponse(cause pfcpType.Cause) (pfcp.PFCPSessionReportResponse, error) {
	msg := pfcp.PFCPSessionReportResponse{}

	msg.Cause = &cause

	return msg, nil
}

func BuildPfcpHeartbeatRequest() (pfcp.HeartbeatRequest, error) {
	msg := pfcp.HeartbeatRequest{
		RecoveryTimeStamp: &pfcpType.RecoveryTimeStamp{
			RecoveryTimeStamp: udp.ServerStartTime,
		},
	}

	return msg, nil
}
