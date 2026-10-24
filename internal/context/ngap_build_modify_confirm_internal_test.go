package context

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

// The QoS Flow Modify Confirm List is mandatory with at least one item and the
// UL NG-U UP TNL Information needs a default data path (TS 38.413 9.3.4.7).
// When either is missing the builder must report an error instead of
// panicking or encoding an undecodable transfer.
func TestBuildPDUSessionResourceModifyConfirmTransferNothingToConfirm(t *testing.T) {
	defaultFlowOnly := newTestUPTunnel()
	defaultFlowOnly.DataPathPool.GetDefaultPath().FirstDPNode.DownLinkTunnel.PDR = &PDR{
		Precedence: 255,
		QER:        []*QER{{QFI: &pfcptype.QFI{QFI: 1}}},
	}

	noDefaultPath := newTestUPTunnel()
	noDefaultPath.DataPathPool[1].IsDefaultPath = false
	noDefaultPath.DataPathPool[1].FirstDPNode.DownLinkTunnel.PDR = &PDR{
		Precedence: 100,
		QER:        []*QER{{QFI: &pfcptype.QFI{QFI: 2}}},
	}

	for name, tunnel := range map[string]*UPTunnel{
		"tunnel without data path":     NewUPTunnel(),
		"only the default QoS flow":    defaultFlowOnly,
		"QoS flow but no default path": noDefaultPath,
	} {
		t.Run(name, func(t *testing.T) {
			var buf []byte
			var err error
			require.NotPanics(t, func() {
				buf, err = BuildPDUSessionResourceModifyConfirmTransfer(newGSMTestContext(), tunnel, 0x00000103)
			})
			require.Error(t, err, "built transfer %x", buf)
			require.Nil(t, buf)
		})
	}
}
