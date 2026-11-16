package processor

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	smf_context "github.com/free5gc/smf/internal/context"
)

// The release of an SM context is notified to the AMF once, however many
// times it is removed.
func TestTheReleaseOfAnSMContextIsNotifiedOnce(t *testing.T) {
	e := newRelCompleteTestEnv(t)
	const notifyPath = "/namf-callback/v1/smContextStatus/imsi-208930000000260/5"
	var notifications atomic.Int32
	amf := newUnencryptedHTTP2TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != notifyPath {
			http.NotFound(w, r)
			return
		}
		notifications.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	e.sm.SmStatusNotifyUri = amf.URL + notifyPath

	// A duplicate PDU session ID removes the SM context at the end of the update
	// with the duplicate cause, then twice for the N2 release response.
	for range 3 {
		e.p.sendSMContextStatusNotificationAndRemoveSMContext(e.sm, true)
	}

	require.Nil(t, smf_context.GetSMContextByRef(e.sm.Ref))
	require.Equal(t, int32(1), notifications.Load())
}
