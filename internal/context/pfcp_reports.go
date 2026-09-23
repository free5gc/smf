package context

import (
	"errors"
	"fmt"

	"github.com/wmnsk/go-pfcp/ie"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

// HandleReports converts every valid grouped go-pfcp Usage Report IE into the
// charging domain records consumed by the CHF flow. It returns the decode
// errors after preserving the valid reports in the same PFCP message.
func (smContext *SMContext) HandleReports(
	reports []*ie.IE,
	nodeID pfcptype.NodeID,
	reportType models.Chf_ConvCharging_TriggerType,
) error {
	return smContext.handleReports(reports, nodeID, reportType, false)
}

// HandleReportsAtomically decodes the complete set before changing charging
// state. It is used for Session Report Requests, where an error response must
// not accompany a partially applied request.
func (smContext *SMContext) HandleReportsAtomically(
	reports []*ie.IE,
	nodeID pfcptype.NodeID,
	reportType models.Chf_ConvCharging_TriggerType,
) error {
	return smContext.handleReports(reports, nodeID, reportType, true)
}

func (smContext *SMContext) handleReports(
	reports []*ie.IE,
	nodeID pfcptype.NodeID,
	reportType models.Chf_ConvCharging_TriggerType,
	atomicBatch bool,
) error {
	upf := RetrieveUPFNodeByNodeID(nodeID)
	if upf == nil {
		return fmt.Errorf("UPF for Node ID %s not found", nodeID.String())
	}

	decoded := make([]UsageReport, 0, len(reports))
	decodeErrors := make([]error, 0)
	for index, grouped := range reports {
		report, err := usageReportFromIE(grouped, upf.UUID(), reportType)
		if err != nil {
			decodeErrors = append(decodeErrors, fmt.Errorf("usage report[%d]: %w", index, err))
			continue
		}
		decoded = append(decoded, report)
	}
	if atomicBatch && len(decodeErrors) != 0 {
		return errors.Join(decodeErrors...)
	}
	for _, report := range decoded {
		logger.PduSessLog.Tracef(
			"[HandleReports] URRID=%d, UpfId=%s, ReportType=%s, TotalVol=%d, UlVol=%d, DlVol=%d",
			report.UrrId, report.UpfId, report.ReportTpye, report.TotalVolume,
			report.UplinkVolume, report.DownlinkVolume,
		)
		smContext.UrrReports = append(smContext.UrrReports, report)
	}
	return errors.Join(decodeErrors...)
}

func usageReportFromIE(
	grouped *ie.IE,
	upfID string,
	override models.Chf_ConvCharging_TriggerType,
) (UsageReport, error) {
	if grouped == nil {
		return UsageReport{}, fmt.Errorf("nil grouped IE")
	}
	children, err := grouped.UsageReport()
	if err != nil {
		return UsageReport{}, fmt.Errorf("decode grouped IE: %w", err)
	}

	var urrIDIE, volumeIE, triggerIE *ie.IE
	for _, child := range children {
		if child == nil {
			continue
		}
		switch child.Type {
		case ie.URRID:
			urrIDIE = child
		case ie.VolumeMeasurement:
			volumeIE = child
		case ie.UsageReportTrigger:
			triggerIE = child
		}
	}
	if urrIDIE == nil {
		return UsageReport{}, fmt.Errorf("missing URR ID")
	}
	urrID, err := urrIDIE.URRID()
	if err != nil {
		return UsageReport{}, fmt.Errorf("decode URR ID: %w", err)
	}
	if triggerIE == nil {
		return UsageReport{}, fmt.Errorf("missing Usage Report Trigger for URR ID %d", urrID)
	}
	if _, err = triggerIE.UsageReportTrigger(); err != nil {
		return UsageReport{}, fmt.Errorf("decode Usage Report Trigger for URR ID %d: %w", urrID, err)
	}

	report := UsageReport{
		UrrId:      urrID,
		UpfId:      upfID,
		ReportTpye: identityTriggerType(triggerIE),
	}
	if override != "" {
		report.ReportTpye = override
	}
	if volumeIE == nil {
		logger.PduSessLog.Warnf("Usage Report missing Volume Measurement for URRID[%d]", urrID)
		return report, nil
	}
	volume, err := volumeIE.VolumeMeasurement()
	if err != nil {
		return UsageReport{}, fmt.Errorf("decode Volume Measurement for URR ID %d: %w", urrID, err)
	}
	report.TotalVolume = volume.TotalVolume
	report.UplinkVolume = volume.UplinkVolume
	report.DownlinkVolume = volume.DownlinkVolume
	report.TotalPktNum = volume.TotalNumberOfPackets
	report.UplinkPktNum = volume.UplinkNumberOfPackets
	report.DownlinkPktNum = volume.DownlinkNumberOfPackets
	return report, nil
}

func identityTriggerType(trigger *ie.IE) models.Chf_ConvCharging_TriggerType {
	switch {
	case trigger.HasVOLTH():
		return models.Chf_ConvCharging_TriggerType_QUOTA_THRESHOLD
	case trigger.HasVOLQU():
		return models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED
	case trigger.HasQUVTI():
		return models.Chf_ConvCharging_TriggerType_VALIDITY_TIME
	case trigger.HasSTART():
		return models.Chf_ConvCharging_TriggerType_START_OF_SERVICE_DATA_FLOW
	case trigger.HasIMMER():
		logger.PduSessLog.Trace("Reports Query by SMF, trigger should be filled later")
		return ""
	case trigger.HasTERMR():
		return models.Chf_ConvCharging_TriggerType_FINAL
	default:
		logger.PduSessLog.Trace("Report is not a charging trigger")
		return ""
	}
}
