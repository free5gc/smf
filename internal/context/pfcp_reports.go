package context

import (
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/logger"
)

func (smContext *SMContext) HandleReports(
	UsageReportRequest []*pfcp.UsageReportPFCPSessionReportRequest,
	UsageReportModification []*pfcp.UsageReportPFCPSessionModificationResponse,
	UsageReportDeletion []*pfcp.UsageReportPFCPSessionDeletionResponse,
	nodeId pfcpType.NodeID, reportTpye models.TriggerType,
) {
	var usageReport UsageReport
	upf := RetrieveUPFNodeByNodeID(nodeId)
	upfId := upf.UUID()

	for _, report := range UsageReportRequest {
		usageReport.UrrId = report.URRID.UrrIdValue
		usageReport.UpfId = upfId
		usageReport.TotalVolume = report.VolumeMeasurement.TotalVolume
		usageReport.UplinkVolume = report.VolumeMeasurement.UplinkVolume
		usageReport.DownlinkVolume = report.VolumeMeasurement.DownlinkVolume
		usageReport.TotalPktNum = report.VolumeMeasurement.TotalPktNum
		usageReport.UplinkPktNum = report.VolumeMeasurement.UplinkPktNum
		usageReport.DownlinkPktNum = report.VolumeMeasurement.DownlinkPktNum
		usageReport.ReportTpye = identityTriggerType(report.UsageReportTrigger)

		if reportTpye != "" {
			usageReport.ReportTpye = reportTpye
		}

		smContext.UrrReports = append(smContext.UrrReports, usageReport)
	}
	for _, report := range UsageReportModification {
		usageReport.UrrId = report.URRID.UrrIdValue
		usageReport.UpfId = upfId
		usageReport.TotalVolume = report.VolumeMeasurement.TotalVolume
		usageReport.UplinkVolume = report.VolumeMeasurement.UplinkVolume
		usageReport.DownlinkVolume = report.VolumeMeasurement.DownlinkVolume
		usageReport.TotalPktNum = report.VolumeMeasurement.TotalPktNum
		usageReport.UplinkPktNum = report.VolumeMeasurement.UplinkPktNum
		usageReport.DownlinkPktNum = report.VolumeMeasurement.DownlinkPktNum
		usageReport.ReportTpye = identityTriggerType(report.UsageReportTrigger)

		if reportTpye != "" {
			usageReport.ReportTpye = reportTpye
		}

		smContext.UrrReports = append(smContext.UrrReports, usageReport)
	}
	for _, report := range UsageReportDeletion {
		usageReport.UrrId = report.URRID.UrrIdValue
		usageReport.UpfId = upfId
		usageReport.TotalVolume = report.VolumeMeasurement.TotalVolume
		usageReport.UplinkVolume = report.VolumeMeasurement.UplinkVolume
		usageReport.DownlinkVolume = report.VolumeMeasurement.DownlinkVolume
		usageReport.TotalPktNum = report.VolumeMeasurement.TotalPktNum
		usageReport.UplinkPktNum = report.VolumeMeasurement.UplinkPktNum
		usageReport.DownlinkPktNum = report.VolumeMeasurement.DownlinkPktNum
		usageReport.ReportTpye = identityTriggerType(report.UsageReportTrigger)

		if reportTpye != "" {
			usageReport.ReportTpye = reportTpye
		}

		smContext.UrrReports = append(smContext.UrrReports, usageReport)
	}
}

func identityTriggerType(usarTrigger *pfcpType.UsageReportTrigger) models.TriggerType {
	var trigger models.TriggerType

	if usarTrigger.Volth {
		trigger = models.TriggerType_QUOTA_THRESHOLD
	} else if usarTrigger.Volqu {
		trigger = models.TriggerType_QUOTA_EXHAUSTED
	} else if usarTrigger.Quvti {
		trigger = models.TriggerType_VALIDITY_TIME
	} else if usarTrigger.Start {
		trigger = models.TriggerType_START_OF_SERVICE_DATA_FLOW
	} else if usarTrigger.Immer {
		logger.PduSessLog.Trace("Reports Query by SMF, trigger should be filled later")
		return ""
	} else if usarTrigger.Termr {
		trigger = models.TriggerType_FINAL
	} else {
		logger.PduSessLog.Trace("Report is not a charging trigger")
		return ""
	}

	return trigger
}
