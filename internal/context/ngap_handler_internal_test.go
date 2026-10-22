package context

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHandleHandoverRequestAcknowledgeTransferClassifiesErrors documents the
// fix for the bug where every error returned by
// HandleHandoverRequestAcknowledgeTransfer was mapped by the SBI processor to
// 503 UPF_NOT_AVAILABLE, even when the real cause was a malformed or
// incomplete NGAP transfer IE from the AMF/gNB (a protocol error that retrying
// the handover will not fix) rather than a genuine UPF failure.
//
// Callers (internal/sbi/processor/pdu_session.go) distinguish the two cases
// with errors.Is(err, ErrN2SmTransferProtocol). This test pins that contract:
// a decode failure is classified as a protocol error, while a genuine
// UPF-side configuration error (e.g. no default-path AN UPF available) is
// not.
func TestHandleHandoverRequestAcknowledgeTransferClassifiesErrors(t *testing.T) {
	t.Run("undecodable NGAP transfer is a protocol error", func(t *testing.T) {
		// Too short to decode a HandoverRequestAcknowledgeTransfer: this must
		// fail in ngapie.UnmarshalBinary itself.
		ctx := &SMContext{}
		err := HandleHandoverRequestAcknowledgeTransfer([]byte{0x00}, ctx)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrN2SmTransferProtocol),
			"decode failure must be classified as ErrN2SmTransferProtocol, got: %v", err)
	})

	t.Run("missing mandatory DLNGUUPTNLInformation choice is a protocol error", func(t *testing.T) {
		// A HandoverRequestAcknowledgeTransfer where the DLNGUUPTNLInformation
		// choice does not resolve to a GTPTunnel (e.g. an unsupported/extension
		// choice) decodes successfully but must still be treated as a protocol
		// error, matching the explicit check in HandleHandoverRequestAcknowledgeTransfer.
		ctx := &SMContext{}
		err := HandleHandoverRequestAcknowledgeTransfer(nil, ctx)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrN2SmTransferProtocol),
			"empty transfer must be classified as ErrN2SmTransferProtocol, got: %v", err)
	})

	t.Run("genuine UPF configuration failure is not a protocol error", func(t *testing.T) {
		// A well-formed Handover Request Acknowledge Transfer carrying both a
		// DL NG-U tunnel and a DL forwarding tunnel (golden bytes reused from
		// github.com/free5gc/ngap/ie's own
		// TestHandoverRequestAcknowledgeTransferMarshalBinary "Case 1"), but
		// where the SMContext has no default-path AN UPF configured for
		// indirect forwarding. This is a genuine UPF/local-state problem, not
		// an AMF/gNB protocol error, and must not be classified as one.
		golden := []byte{
			0x40, 0x07, 0xc0, 0xac, 0x10, 0x1f, 0x33, 0x00, 0x00, 0x00, 0x01, 0x01, 0xf0, 0xac, 0x10, 0x1f,
			0x33, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01,
		}
		ctx := &SMContext{
			Tunnel:           NewUPTunnel(),
			DLForwardingType: IndirectForwarding,
		}
		err := HandleHandoverRequestAcknowledgeTransfer(golden, ctx)
		require.Error(t, err)
		require.False(t, errors.Is(err, ErrN2SmTransferProtocol),
			"genuine UPF configuration failure must not be classified as ErrN2SmTransferProtocol, got: %v", err)
	})
}
