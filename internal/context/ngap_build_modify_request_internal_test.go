package context

import (
	"testing"

	"github.com/stretchr/testify/require"

	ngapie "github.com/free5gc/ngap/ie"
	"github.com/free5gc/openapi/models"
)

// A PDU session modification that adds or modifies no QoS flow (for example a
// UE-requested modification without a QoS rule, or one whose only flow is
// already set up) must still yield a decodable transfer: the optional QoS Flow
// Add or Modify Request List (SIZE(1..maxnoofQoSFlows)) is left out.
func TestBuildPDUSessionResourceModifyRequestTransferWithoutQosFlow(t *testing.T) {
	alreadySetUp := NewQoSFlow(2, &models.Pcf_SMPolCtrl_QosData{Var5qi: 9, Arp: &models.Arp{PriorityLevel: 8}})
	alreadySetUp.State = QoSFlowSet

	for name, flows := range map[string]map[uint8]*QoSFlow{
		"no additional QoS flow":          {},
		"additional QoS flow already set": {2: alreadySetUp},
	} {
		t.Run(name, func(t *testing.T) {
			smContext := newGSMTestContext()
			smContext.AdditionalQosFlows = flows

			got, err := BuildPDUSessionResourceModifyRequestTransfer(smContext)
			require.NoError(t, err)

			var decoded ngapie.PDUSessionResourceModifyRequestTransfer
			require.NoError(t, ngapie.UnmarshalBinary(got, &decoded), "transfer %x is undecodable", got)
			require.NotNil(t, decoded.ProtocolIEs)
			require.Empty(t, decoded.ProtocolIEs.List)
			// empty extensible SEQUENCE + empty ProtocolIE-Container (SIZE(0..maxProtocolIEs))
			require.Equal(t, []byte{0x00, 0x00, 0x00}, got)
		})
	}
}
