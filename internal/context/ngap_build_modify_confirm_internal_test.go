package context

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/ngap/aper"
	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

// The QoS Flow Modify Confirm List is mandatory with at least one item and the
// UL NG-U UP TNL Information needs a data path (TS 38.413 9.3.4.7). When
// either is missing the builder must report an error instead of panicking or
// encoding an undecodable transfer.
func TestBuildPDUSessionResourceModifyConfirmTransferNothingToConfirm(t *testing.T) {
	defaultFlowOnly := newTestUPTunnel()
	defaultFlowOnly.DataPathPool.GetDefaultPath().FirstDPNode.DownLinkTunnel.PDR = &PDR{
		Precedence: 255,
		QER:        []*QER{{QFI: &pfcptype.QFI{QFI: 1}}},
	}

	for name, tunnel := range map[string]*UPTunnel{
		"tunnel without data path":  NewUPTunnel(),
		"only the default QoS flow": defaultFlowOnly,
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

// A split tunnel has a default data path only for a PCC rule of precedence
// 255. Without one, the UPF end of the tunnel is that of its other paths.
func TestBuildPDUSessionResourceModifyConfirmTransferWithoutDefaultPath(t *testing.T) {
	tunnel := newTestUPTunnel()
	tunnel.DataPathPool[1].IsDefaultPath = false
	tunnel.DataPathPool[1].FirstDPNode.DownLinkTunnel.PDR = &PDR{
		Precedence: 100,
		QER:        []*QER{{QFI: &pfcptype.QFI{QFI: 2}}},
	}

	buf, err := BuildPDUSessionResourceModifyConfirmTransfer(newGSMTestContext(), tunnel, 0x00000103)
	require.NoError(t, err)
	var confirm ngapie.PDUSessionResourceModifyConfirmTransfer
	require.NoError(t, ngapie.UnmarshalBinary(buf, &confirm))
	ul, ok := confirm.ULNGUUPTNLInformation.Choice.(*ngapie.GTPTunnel)
	require.True(t, ok)
	require.Equal(t, aper.OctetString{0x00, 0x00, 0x01, 0x03}, ul.GTPTEID.Value)
	require.Equal(t, net.ParseIP("192.168.179.1").To4(), net.IP(ul.TransportLayerAddress.Value.Bytes))
}
