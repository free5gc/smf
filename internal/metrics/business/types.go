package business

// Global metric information
const (
	SUBSYSTEM_NAME      = "smf_business"
	PDU_SESSION_METRICS = "pdu-session"
	PFCP_METRICS        = "pfcp"
)

// Collectors information
const (
	PDU_SESSION_ESTABLISHMENT_COUNTER_NAME = "pdu_session_establishment_total"
	PDU_SESSION_ESTABLISHMENT_COUNTER_DESC = "Count of PDU session establishment outcomes in the SMF, " +
		"by status and cause"
	PDU_SESSION_RELEASE_COUNTER_NAME = "pdu_session_release_total"
	PDU_SESSION_RELEASE_COUNTER_DESC = "Count of PDU session release outcomes (PFCP session deletion on the UPFs) " +
		"in the SMF, by trigger, status and cause"
	PDU_SESSION_STATE_GAUGE_NAME = "pdu_session_current_count"
	PDU_SESSION_STATE_GAUGE_DESC = "Number of PDU session (SM) contexts currently held by the SMF, by SM context state"

	PFCP_REQUEST_COUNTER_NAME       = "pfcp_request_total"
	PFCP_REQUEST_COUNTER_DESC       = "Count of PFCP requests sent by the SMF to UPFs, by message type, UPF and result"
	PFCP_REQUEST_DURATION_HIST_NAME = "pfcp_request_duration_seconds"
	PFCP_REQUEST_DURATION_HIST_DESC = "Time from sending a PFCP request to a UPF until its response is received"
)

// Label names
const (
	// PDU session
	PDU_SESSION_STATUS_LABEL  = "status"
	PDU_SESSION_CAUSE_LABEL   = "cause"
	PDU_SESSION_TRIGGER_LABEL = "trigger"
	PDU_SESSION_STATE_LABEL   = "state"

	// PFCP
	PFCP_MESSAGE_TYPE_LABEL = "message_type"
	// PFCP_UPF_LABEL holds the UPF node ID (as an IP address). Its values are bounded by the UPFs
	// configured in the userplaneInformation section of smfcfg.yaml.
	PFCP_UPF_LABEL    = "upf"
	PFCP_RESULT_LABEL = "result"
)

// Metrics Values
const (
	// Release triggers
	RELEASE_TRIGGER_UE_REQUESTED  = "ue_requested"
	RELEASE_TRIGGER_AMF_REQUESTED = "amf_requested"
	// RELEASE_TRIGGER_DUPLICATE_SESSION_ID is an AMF update with cause REL_DUE_TO_DUPLICATE_SESSION_ID.
	RELEASE_TRIGGER_DUPLICATE_SESSION_ID = "duplicate_session_id"
	// RELEASE_TRIGGER_DUPLICATE_SM_CONTEXT is the local release of an existing SM context when a new
	// SM context create arrives for the same SUPI and PDU session ID.
	RELEASE_TRIGGER_DUPLICATE_SM_CONTEXT = "duplicate_sm_context"

	// PFCP message types. Only requests the SMF sends are listed. Anything else is reported as other.
	PFCP_MSG_HEARTBEAT             = "heartbeat"
	PFCP_MSG_ASSOCIATION_SETUP     = "association_setup"
	PFCP_MSG_ASSOCIATION_RELEASE   = "association_release"
	PFCP_MSG_SESSION_ESTABLISHMENT = "session_establishment"
	PFCP_MSG_SESSION_MODIFICATION  = "session_modification"
	PFCP_MSG_SESSION_DELETION      = "session_deletion"
	PFCP_MSG_OTHER                 = "other"

	// PFCP results
	PFCP_RESULT_SUCCESS  = "success"
	PFCP_RESULT_REJECTED = "rejected"
	PFCP_RESULT_TIMEOUT  = "timeout"
	// PFCP_RESULT_ERROR covers every other failure: an unusable response, a canceled association
	// context, a stopped PFCP server or an invalid request.
	PFCP_RESULT_ERROR = "error"
)

// Potential causes. Establishment failures that the SMF reports to the AMF use the 3GPP cause of
// the error it returns (for example DNN_NOT_SUPPORTED, see pkg/errors). The causes below cover
// failures that have no 3GPP cause, mostly because they happen after the SMF answered the AMF.
const (
	PDU_SESSION_EMPTY_CAUSE = ""

	ESTABLISHMENT_UDM_TOKEN_FAILURE    = "UDM_ACCESS_TOKEN_FAILURE"
	ESTABLISHMENT_PFCP_FAILURE         = "PFCP_ESTABLISHMENT_FAILURE"
	ESTABLISHMENT_ACCEPT_BUILD_FAILURE = "ACCEPT_BUILD_FAILURE"
	ESTABLISHMENT_AMF_TOKEN_FAILURE    = "AMF_ACCESS_TOKEN_FAILURE"
	ESTABLISHMENT_N1N2_FAILURE         = "N1N2_TRANSFER_FAILURE"

	RELEASE_PFCP_DELETION_FAILURE = "PFCP_SESSION_DELETION_FAILURE"
)

var pduSessionMetricsEnabled bool

func IsPduSessionMetricsEnabled() bool {
	return pduSessionMetricsEnabled
}

func EnablePduSessionMetrics() {
	pduSessionMetricsEnabled = true
}

var pfcpMetricsEnabled bool

func IsPfcpMetricsEnabled() bool {
	return pfcpMetricsEnabled
}

func EnablePfcpMetrics() {
	pfcpMetricsEnabled = true
}
