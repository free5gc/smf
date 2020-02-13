package smf_context

import (
	"gofree5gc/lib/nas/nasMessage"
)

func (smContext *SMContext) HandlePDUSessionEstablishmentRequest(req *nasMessage.PDUSessionEstablishmentRequest) {
	// Retrieve PDUSessionID
	smContext.PDUSessionID = int32(req.PDUSessionID.GetPDUSessionID())

	// Handle PDUSessionType
	if req.PDUSessionType != nil {
		smContext.SelectedPDUSessionType = req.PDUSessionType.GetPDUSessionTypeValue()
	} else {
		// Default to IPv4
		smContext.SelectedPDUSessionType = nasMessage.PDUSessionTypeIPv4
	}
}

func (smContext *SMContext) HandlePDUSessionReleaseRequest(req *nasMessage.PDUSessionReleaseRequest) {
}
