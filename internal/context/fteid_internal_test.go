package context

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func TestNameChosenFTEID(t *testing.T) {
	uplink := &pfcptype.FTEID{V4: true, Teid: 0x11, Ipv4Address: net.ParseIP("192.0.2.8").To4()}
	asking := func(fteid pfcptype.FTEID) *PDR {
		return &PDR{PDI: PDI{
			SourceInterface: pfcptype.SourceInterface{InterfaceValue: pfcptype.SourceInterfaceAccess},
			LocalFTeid:      &fteid,
		}}
	}

	// Nothing chosen yet: the PDR asks.
	session := &PFCPSessionContext{}
	pdr := asking(pfcptype.FTEID{Ch: true, Chid: true, V4: true, ChooseId: UplinkChooseID})
	session.NameChosenFTEID(pdr)
	require.True(t, pdr.PDI.LocalFTeid.Ch)

	// Once chosen, a PDR that asks by the same CHOOSE ID names it, as a copy.
	session.SetChosenFTEID(UplinkChooseID, uplink)
	session.NameChosenFTEID(pdr)
	require.Equal(t, uplink, pdr.PDI.LocalFTeid)
	require.NotSame(t, uplink, pdr.PDI.LocalFTeid)

	// A PDR that asks for an F-TEID of its own, with no CHOOSE ID, keeps asking.
	own := asking(pfcptype.FTEID{Ch: true, V4: true})
	session.NameChosenFTEID(own)
	require.Equal(t, &pfcptype.FTEID{Ch: true, V4: true}, own.PDI.LocalFTeid)

	// An F-TEID of the SMF's is left as it is.
	smfs := asking(pfcptype.FTEID{V4: true, Teid: 0x33})
	session.NameChosenFTEID(smfs)
	require.Equal(t, uint32(0x33), smfs.PDI.LocalFTeid.Teid)

	// The session at the UPF is gone: so are the F-TEIDs that it chose.
	session.ForgetRemoteSession()
	pdr = asking(pfcptype.FTEID{Ch: true, Chid: true, V4: true, ChooseId: UplinkChooseID})
	session.NameChosenFTEID(pdr)
	require.True(t, pdr.PDI.LocalFTeid.Ch)
}
