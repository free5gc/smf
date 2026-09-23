package context_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wmnsk/go-pfcp/ie"

	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func TestHandleReportsPreservesValidReportsWhenOneIsMalformed(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.101").To4(),
	}
	context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { context.RemoveUPFNodeByNodeID(nodeID) })

	smContext := &context.SMContext{}
	reports := []*ie.IE{
		validUsageReport(101),
		ie.NewUsageReportWithinSessionModificationResponse(
			ie.NewUsageReportTrigger(0, 0x08),
		),
		validUsageReport(102),
	}

	err := smContext.HandleReports(reports, nodeID, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage report[1]")
	require.Len(t, smContext.UrrReports, 2)
	require.Equal(t, uint32(101), smContext.UrrReports[0].UrrId)
	require.Equal(t, uint32(102), smContext.UrrReports[1].UrrId)
}

func TestHandleReportsAtomicallyRejectsPartialBatch(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.102").To4(),
	}
	context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { context.RemoveUPFNodeByNodeID(nodeID) })

	smContext := &context.SMContext{}
	err := smContext.HandleReportsAtomically([]*ie.IE{
		validUsageReport(201),
		ie.NewUsageReportWithinSessionReportRequest(
			ie.NewUsageReportTrigger(0, 0x08),
		),
	}, nodeID, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "usage report[1]")
	require.Empty(t, smContext.UrrReports)
}

func validUsageReport(urrID uint32) *ie.IE {
	return ie.NewUsageReportWithinSessionModificationResponse(
		ie.NewURRID(urrID),
		ie.NewUsageReportTrigger(0, 0x08),
		ie.NewVolumeMeasurement(0x07, 300, 100, 200, 0, 0, 0),
	)
}
