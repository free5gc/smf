package message_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/smf/internal/context"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
)

func TestSharedQERRetirementAccountsForUnchangedPDRs(t *testing.T) {
	initDirectBuilderTestContext(t)
	sm := context.NewSMContext("imsi-208930000000120", 10)
	t.Cleanup(func() { context.RemoveSMContext(sm.Ref) })
	qer := &context.QER{QERID: 10, State: context.RULE_CREATE}
	removed := &context.PDR{PDRID: 1, QER: []*context.QER{qer}, State: context.RULE_REMOVE}
	live := &context.PDR{PDRID: 2, QER: []*context.QER{qer}, State: context.RULE_CREATE}
	sm.PFCPContext["192.0.2.120"] = &context.PFCPSessionContext{
		LocalSEID: 123, RemoteSEID: 456, PDRs: map[uint16]*context.PDR{1: removed, 2: live},
	}
	request, err := pfcp_message.BuildSessionModificationRequest("192.0.2.120", "upf", sm,
		[]*context.PDR{removed}, nil, nil, []*context.QER{qer}, nil)
	require.NoError(t, err)
	require.Len(t, request.RemovePDR, 1)
	require.Empty(t, request.RemoveQER, "unchanged PDR still owns the QER")
	delete(sm.PFCPContext["192.0.2.120"].PDRs, removed.PDRID)
	live.State = context.RULE_REMOVE
	request, err = pfcp_message.BuildSessionModificationRequest("192.0.2.120", "upf", sm,
		[]*context.PDR{live}, nil, nil, []*context.QER{qer}, nil)
	require.NoError(t, err)
	require.Len(t, request.RemoveQER, 1)
}
