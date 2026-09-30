package context

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/free5gc/ngap/aper"
	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
)

const DefaultNonGBR5QI = 9

// ueAmbrToInt64 replaces the removed ngapConvert.UEAmbrToInt64 helper and
// keeps its exact conversion behavior.
func ueAmbrToInt64(modelAmbr string) int64 {
	tok := strings.Split(modelAmbr, " ")
	ambr, err := strconv.ParseFloat(tok[0], 64)
	if err != nil {
		logger.CtxLog.Warnf("Parse AMBR failed %+v", err)
		return 0
	}
	var unit float64
	switch tok[1] {
	case "bps":
		unit = 1.0
	case "Kbps":
		unit = 1000.0
	case "Mbps":
		unit = 1000000.0
	case "Gbps":
		unit = 1000000000.0
	case "Tbps":
		unit = 1000000000000.0
	default:
		unit = 1.0
	}
	return int64(ambr * unit)
}

// ipv4AddressToNgap replaces the IPv4 branch of the removed
// ngapConvert.IPAddressToNgap helper.
func ipv4AddressToNgap(ipv4Addr string) *ngapie.TransportLayerAddress {
	ipv4NetIP := net.ParseIP(ipv4Addr).To4()
	if ipv4NetIP == nil {
		logger.CtxLog.Warnf("ipv4AddressToNgap: invalid IPv4 address %q", ipv4Addr)
		return &ngapie.TransportLayerAddress{}
	}
	return &ngapie.TransportLayerAddress{
		Value: aper.BitString{
			Bytes:     ipv4NetIP,
			BitLength: 32,
		},
	}
}

func newGTPTunnelUPTransport(transportAddr []byte, teid []byte) *ngapie.UPTransportLayerInformation {
	return &ngapie.UPTransportLayerInformation{
		Choice: &ngapie.GTPTunnel{
			TransportLayerAddress: &ngapie.TransportLayerAddress{
				Value: aper.BitString{
					Bytes:     transportAddr,
					BitLength: uint64(len(transportAddr) * 8),
				},
			},
			GTPTEID: &ngapie.GTPTEID{Value: teid},
		},
	}
}

func buildSecurityIndication(ctx *SMContext) *ngapie.SecurityIndication {
	upSecurity := ctx.UpSecurity
	maximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink := ctx.
		MaximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink

	securityIndication := &ngapie.SecurityIndication{
		IntegrityProtectionIndication:       new(ngapie.IntegrityProtectionIndication),
		ConfidentialityProtectionIndication: new(ngapie.ConfidentialityProtectionIndication),
	}

	switch upSecurity.UpIntegr {
	case models.UpIntegrity_REQUIRED:
		securityIndication.IntegrityProtectionIndication.Value = ngapie.
			IntegrityProtectionIndicationPresentRequired
	case models.UpIntegrity_PREFERRED:
		securityIndication.IntegrityProtectionIndication.Value = ngapie.
			IntegrityProtectionIndicationPresentPreferred
	case models.UpIntegrity_NOT_NEEDED:
		securityIndication.IntegrityProtectionIndication.Value = ngapie.
			IntegrityProtectionIndicationPresentNotNeeded
	}
	switch upSecurity.UpConfid {
	case models.UpConfidentiality_REQUIRED:
		securityIndication.ConfidentialityProtectionIndication.Value = ngapie.
			ConfidentialityProtectionIndicationPresentRequired
	case models.UpConfidentiality_PREFERRED:
		securityIndication.ConfidentialityProtectionIndication.Value = ngapie.
			ConfidentialityProtectionIndicationPresentPreferred
	case models.UpConfidentiality_NOT_NEEDED:
		securityIndication.ConfidentialityProtectionIndication.Value = ngapie.
			ConfidentialityProtectionIndicationPresentNotNeeded
	}
	// Present only when Integrity Indication within the Security Indication is set to "required" or "preferred"
	integrityProtectionInd := securityIndication.IntegrityProtectionIndication.Value
	if integrityProtectionInd == ngapie.IntegrityProtectionIndicationPresentRequired ||
		integrityProtectionInd == ngapie.IntegrityProtectionIndicationPresentPreferred {
		securityIndication.MaximumIntegrityProtectedDataRateUL = new(ngapie.MaximumIntegrityProtectedDataRate)
		switch maximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink {
		case models.Smf_PDUSess_MaxIntegrityProtectedDataRate_MAX_UE_RATE:
			securityIndication.MaximumIntegrityProtectedDataRateUL.Value = ngapie.
				MaximumIntegrityProtectedDataRatePresentMaximumUERate
		case models.Smf_PDUSess_MaxIntegrityProtectedDataRate_64_KBPS:
			securityIndication.MaximumIntegrityProtectedDataRateUL.Value = ngapie.
				MaximumIntegrityProtectedDataRatePresentBitrate64kbs
		}
	}
	return securityIndication
}

func BuildPDUSessionResourceSetupRequestTransfer(ctx *SMContext) ([]byte, error) {
	ANUPF := ctx.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
	UpNode := ANUPF.UPF
	teidOct := make([]byte, 4)
	teidOctForSplitPDUSession := make([]byte, 4)
	binary.BigEndian.PutUint32(teidOct, ctx.LocalULTeid)
	binary.BigEndian.PutUint32(teidOctForSplitPDUSession, ctx.LocalULTeidForSplitPDUSession)

	ieList := []ngapie.PDUSessionResourceSetupRequestTransferIEs{}

	// PDU Session Aggregate Maximum Bit Rate
	// This IE is Conditional and shall be present when at least one NonGBR QoS flow is being setup.
	// TODO: should check if there is at least one NonGBR QoS flow
	sessRule := ctx.SelectedSessionRule()
	if sessRule == nil || sessRule.AuthSessAmbr == nil {
		return nil, fmt.Errorf("no PDU Session AMBR")
	}
	ieList = append(ieList, ngapie.PDUSessionResourceSetupRequestTransferIEs{
		PDUSessionAggregateMaximumBitRate: &ngapie.PDUSessionAggregateMaximumBitRate{
			PDUSessionAggregateMaximumBitRateDL: &ngapie.BitRate{
				Value: ueAmbrToInt64(sessRule.AuthSessAmbr.Downlink),
			},
			PDUSessionAggregateMaximumBitRateUL: &ngapie.BitRate{
				Value: ueAmbrToInt64(sessRule.AuthSessAmbr.Uplink),
			},
		},
	})

	n3IP, err := UpNode.N3Interfaces[0].IP(ctx.SelectedPDUSessionType)
	if err != nil {
		return nil, err
	}
	ieList = append(ieList,
		// UL NG-U UP TNL Information
		ngapie.PDUSessionResourceSetupRequestTransferIEs{
			ULNGUUPTNLInformation: newGTPTunnelUPTransport(n3IP, teidOct),
		},
		// Additional UL NG-U UP TNL Information
		ngapie.PDUSessionResourceSetupRequestTransferIEs{
			AdditionalULNGUUPTNLInformation: &ngapie.UPTransportLayerInformationList{
				List: []ngapie.UPTransportLayerInformationItem{
					{
						NGUUPTNLInformation: newGTPTunnelUPTransport(n3IP, teidOctForSplitPDUSession),
					},
				},
			},
		},
		// PDU Session Type
		ngapie.PDUSessionResourceSetupRequestTransferIEs{
			PDUSessionType: &ngapie.PDUSessionType{
				Value: ngapie.PDUSessionTypePresentIpv4,
			},
		})

	// QoS Flow Setup Request List
	// use Default 5qi, arp
	authDefQos := sessRule.AuthDefQos
	qosFlowSetupRequestList := &ngapie.QosFlowSetupRequestList{
		List: []ngapie.QosFlowSetupRequestItem{
			{
				QosFlowIdentifier: &ngapie.QosFlowIdentifier{
					Value: int64(sessRule.DefQosQFI),
				},
				QosFlowLevelQosParameters: &ngapie.QosFlowLevelQosParameters{
					QosCharacteristics: &ngapie.QosCharacteristics{
						Choice: &ngapie.NonDynamic5QIDescriptor{
							FiveQI: &ngapie.FiveQI{
								Value: int64(authDefQos.Var5qi),
							},
						},
					},
					AllocationAndRetentionPriority: &ngapie.AllocationAndRetentionPriority{
						PriorityLevelARP: &ngapie.PriorityLevelARP{
							Value: int64(authDefQos.Arp.PriorityLevel),
						},
						PreEmptionCapability: &ngapie.PreEmptionCapability{
							Value: ngapie.PreEmptionCapabilityPresentShallNotTriggerPreEmption,
						},
						PreEmptionVulnerability: &ngapie.PreEmptionVulnerability{
							Value: ngapie.PreEmptionVulnerabilityPresentNotPreEmptable,
						},
					},
				},
			},
		},
	}

	for _, qosFlow := range ctx.AdditionalQosFlows {
		if qosDesc, errBuild := qosFlow.BuildNgapQosFlowSetupRequestItem(); errBuild != nil {
			return nil, fmt.Errorf("encode BuildNgapQosFlowSetupRequestItem failed: %s", errBuild)
		} else {
			qosFlowSetupRequestList.List = append(qosFlowSetupRequestList.List, qosDesc)
		}
	}

	ieList = append(ieList, ngapie.PDUSessionResourceSetupRequestTransferIEs{
		QosFlowSetupRequestList: qosFlowSetupRequestList,
	})

	// Security Indication to NG-RAN (optional) TS 38.413 9.3.1.27
	// Only over 3GPP access TS 23.501 5.10.3
	if ctx.AnType == models.AccessType_3_GPP_ACCESS && ctx.UpSecurity != nil {
		ieList = append(ieList, ngapie.PDUSessionResourceSetupRequestTransferIEs{
			SecurityIndication: buildSecurityIndication(ctx),
		})
	}

	resourceSetupRequestTransfer := ngapie.PDUSessionResourceSetupRequestTransfer{
		ProtocolIEs: &ngapie.ProtocolIEContainerPDUSessionResourceSetupRequestTransferIEs{
			List: ieList,
		},
	}

	if buf, errMarshal := ngapie.MarshalBinary(&resourceSetupRequestTransfer); errMarshal != nil {
		return nil, fmt.Errorf("encode resourceSetupRequestTransfer failed: %s", errMarshal)
	} else {
		return buf, nil
	}
}

func BuildPDUSessionResourceModifyRequestTransfer(ctx *SMContext) ([]byte, error) {
	qosFlowAddOrModifyRequestList := new(ngapie.QosFlowAddOrModifyRequestList)

	for _, qos := range ctx.AdditionalQosFlows {
		if qos.State == QoSFlowUnset || qos.State == QoSFlowToBeModify {
			if qosDesc, err := qos.BuildNgapQosFlowAddOrModifyRequestItem(); err != nil {
				return nil, fmt.Errorf("BuildNgapQosFlowSetupRequestItem failed: %s", err)
			} else {
				qosFlowAddOrModifyRequestList.List = append(qosFlowAddOrModifyRequestList.List, qosDesc)
			}
		}
	}

	resourceModifyRequestTransfer := ngapie.PDUSessionResourceModifyRequestTransfer{
		ProtocolIEs: &ngapie.ProtocolIEContainerPDUSessionResourceModifyRequestTransferIEs{
			List: []ngapie.PDUSessionResourceModifyRequestTransferIEs{
				{
					QosFlowAddOrModifyRequestList: qosFlowAddOrModifyRequestList,
				},
			},
		},
	}

	if buf, err := ngapie.MarshalBinary(&resourceModifyRequestTransfer); err != nil {
		return nil, fmt.Errorf("encode resourceModifyRequestTransfer failed: %s", err)
	} else {
		return buf, nil
	}
}

func BuildPDUSessionResourceModifyConfirmTransfer(
	ctx *SMContext,
	tunnel *UPTunnel,
	localULTeid uint32,
) ([]byte, error) {
	confirmTransfer := ngapie.PDUSessionResourceModifyConfirmTransfer{}

	// QoS Flow Modify Confirm List
	qosList := new(ngapie.QosFlowModifyConfirmList)
	confirmTransfer.QosFlowModifyConfirmList = qosList
	for _, dataPath := range tunnel.DataPathPool {
		if dataPath.Activated {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR
			// The flow we move to secondary gNB will not include precedence=255(default flow).
			// So we do not need to send the flow QFI to RAN
			if DLPDR.Precedence == 255 {
				continue
			}

			for _, qer := range DLPDR.QER {
				qosList.List = append(qosList.List, ngapie.QosFlowModifyConfirmItem{
					QosFlowIdentifier: &ngapie.QosFlowIdentifier{
						Value: int64(qer.QFI.QFI),
					},
				})
			}
		}
	}

	// UL NG-U UP TNL Information
	ANUPF := tunnel.DataPathPool.GetDefaultPath().FirstDPNode
	teidOct := make([]byte, 4)
	binary.BigEndian.PutUint32(teidOct, localULTeid)

	confirmTransfer.ULNGUUPTNLInformation = &ngapie.UPTransportLayerInformation{
		Choice: &ngapie.GTPTunnel{
			TransportLayerAddress: ipv4AddressToNgap(ANUPF.UPF.NodeID.ResolveNodeIdToIp().String()),
			GTPTEID: &ngapie.GTPTEID{
				Value: teidOct,
			},
		},
	}

	if buf, err := ngapie.MarshalBinary(&confirmTransfer); err != nil {
		return nil, fmt.Errorf("encode confirmTransfer failed: %s", err)
	} else {
		return buf, nil
	}
}

// TS 38.413 9.3.4.9
func BuildPathSwitchRequestAcknowledgeTransfer(ctx *SMContext) ([]byte, error) {
	ANUPF := ctx.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
	UpNode := ANUPF.UPF
	teidOct := make([]byte, 4)
	binary.BigEndian.PutUint32(teidOct, ANUPF.UpLinkTunnel.TEID)

	pathSwitchRequestAcknowledgeTransfer := ngapie.PathSwitchRequestAcknowledgeTransfer{}

	// UL NG-U UP TNL Information(optional) TS 38.413 9.3.2.2
	if len(UpNode.N3Interfaces) == 0 {
		return nil, errors.New("no N3 interface found for UPF")
	}
	if n3IP, err := UpNode.N3Interfaces[0].IP(ctx.SelectedPDUSessionType); err != nil {
		return nil, err
	} else {
		pathSwitchRequestAcknowledgeTransfer.ULNGUUPTNLInformation = newGTPTunnelUPTransport(n3IP, teidOct)
	}

	// Received UP security policy mismatch from SMF locally stored TS 33.501 6.6.1
	// Security Indication(optional) TS 38.413 9.3.1.27
	if !ctx.UpSecurityFromPathSwitchRequestSameAsLocalStored {
		pathSwitchRequestAcknowledgeTransfer.SecurityIndication = buildSecurityIndication(ctx)
	}

	// Additional DL NG-U UP TNL Information(optional) TS 38.413 9.3.4.9
	if ctx.NrdcIndicator {
		dcANUPF := ctx.DCTunnel.DataPathPool.GetDefaultPath().FirstDPNode
		dcUpNode, dcUlTeidOct, dcDlTeidOct := dcANUPF.UPF, make([]byte, 4), make([]byte, 4)
		binary.BigEndian.PutUint32(dcUlTeidOct, dcANUPF.UpLinkTunnel.TEID)
		binary.BigEndian.PutUint32(dcDlTeidOct, ctx.DCTunnel.ANInformation.TEID)

		ieExtensions := new(ngapie.ProtocolExtensionContainerPathSwitchRequestAcknowledgeTransferExtIEs)
		pathSwitchRequestAcknowledgeTransfer.IEExtensions = ieExtensions

		if len(dcUpNode.N3Interfaces) == 0 {
			return nil, errors.New("no N3 interface found for DC UPF")
		}
		if n3IP, err := dcUpNode.N3Interfaces[0].IP(ctx.SelectedPDUSessionType); err != nil {
			return nil, err
		} else {
			ieExtensions.List = append(ieExtensions.List, ngapie.PathSwitchRequestAcknowledgeTransferExtIEs{
				Id: &ngapie.ProtocolExtensionID{
					Value: ngapie.ProtocolIEIDAdditionalNGUUPTNLInformation,
				},
				Criticality: &ngapie.Criticality{
					Value: ngapie.CriticalityPresentIgnore,
				},
				AdditionalNGUUPTNLInformation: &ngapie.UPTransportLayerInformationPairList{
					List: []ngapie.UPTransportLayerInformationPairItem{
						{
							ULNGUUPTNLInformation: newGTPTunnelUPTransport(n3IP, dcUlTeidOct),
							DLNGUUPTNLInformation: newGTPTunnelUPTransport(
								ctx.DCTunnel.ANInformation.IPAddress, dcDlTeidOct),
						},
					},
				},
			})
		}
	}

	if buf, err := ngapie.MarshalBinary(&pathSwitchRequestAcknowledgeTransfer); err != nil {
		return nil, err
	} else {
		return buf, nil
	}
}

func BuildPathSwitchRequestUnsuccessfulTransfer(cause ngapie.CauseAlt) (buf []byte, err error) {
	pathSwitchRequestUnsuccessfulTransfer := ngapie.PathSwitchRequestUnsuccessfulTransfer{
		Cause: &ngapie.Cause{
			Choice: cause,
		},
	}

	buf, err = ngapie.MarshalBinary(&pathSwitchRequestUnsuccessfulTransfer)
	if err != nil {
		return nil, err
	}
	return
}

func BuildPDUSessionResourceReleaseCommandTransfer(ctx *SMContext) (buf []byte, err error) {
	resourceReleaseCommandTransfer := ngapie.PDUSessionResourceReleaseCommandTransfer{
		Cause: &ngapie.Cause{
			Choice: &ngapie.CauseNas{
				Value: ngapie.CauseNasPresentNormalRelease,
			},
		},
	}
	buf, err = ngapie.MarshalBinary(&resourceReleaseCommandTransfer)
	if err != nil {
		return nil, err
	}
	return
}

func BuildHandoverCommandTransfer(ctx *SMContext) ([]byte, error) {
	handoverCommandTransfer := ngapie.HandoverCommandTransfer{}

	switch ctx.DLForwardingType {
	case IndirectForwarding:
		ANUPF := ctx.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode
		UpNode := ANUPF.UPF
		teidOct := make([]byte, 4)
		binary.BigEndian.PutUint32(teidOct, ctx.IndirectForwardingTunnel.FirstDPNode.UpLinkTunnel.TEID)

		if n3IP, err := UpNode.N3Interfaces[0].IP(ctx.SelectedPDUSessionType); err != nil {
			return nil, err
		} else {
			handoverCommandTransfer.DLForwardingUPTNLInformation = newGTPTunnelUPTransport(n3IP, teidOct)
		}
	case DirectForwarding:
		handoverCommandTransfer.DLForwardingUPTNLInformation = ctx.DLDirectForwardingTunnel
	}

	if len(ctx.QosFlowsToBeForwarded) > 0 {
		qosFlowList := make([]ngapie.QosFlowToBeForwardedItem, 0, len(ctx.QosFlowsToBeForwarded))
		for _, qfi := range ctx.QosFlowsToBeForwarded {
			qosFlowList = append(qosFlowList, ngapie.QosFlowToBeForwardedItem{
				QosFlowIdentifier: &ngapie.QosFlowIdentifier{Value: qfi},
			})
		}
		handoverCommandTransfer.QosFlowToBeForwardedList = &ngapie.QosFlowToBeForwardedList{
			List: qosFlowList,
		}
	}

	if buf, err := ngapie.MarshalBinary(&handoverCommandTransfer); err != nil {
		return nil, err
	} else {
		return buf, nil
	}
}
