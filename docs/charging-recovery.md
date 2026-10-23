# Charging retry and quota continuation

This stage restores failed Update/Release operations while SMF remains running.
It requires CHF duplicate detection and replay of completed requests. Deploy the
matching CHF request journal/recovery changes before enabling this SMF version.
Release settlement and NF cleanup resume automatically once their owning
UE/AMF lifecycle path permits removal. Initial request deduplication is outside
this change. Recovery is limited to the running SMF process; no database is used.

## Request recovery

The processor retains one charging work item per SMContext.Ref. It captures the
original report batch and original RG/UPFID rule pointers before the first call.
The consumer freezes the complete request, sequence, timestamp, resource reference
and CHF destination. Retries use these snapshots even if discovery/session
metadata changes. A copied response cannot modify the stored request.

Normal BeginChargingUpdate/BeginChargingRelease guards remain. Only the recovery
path can move a failed frozen invocation back into InProgress, under the original
session's SMLock. A worker examines retained work once per second, skips locked
sessions and honors per-item backoff (2 seconds initially, doubling to 5 minutes).
It verifies that the registered SMContext instance and work item still match;
responses cannot be handed to a replacement context with the same SUPI.

Transport/decode failures and HTTP 408/429/5xx may replay the original invocation.
Other HTTP errors and mismatched response sequence numbers retain the operation
for diagnosis without automatic resubmission. Generic OpenAPI errors are decoded
from their raw problem body and actual HTTP status, including response/problem
union models. An error response with no generated ProblemDetails pointer cannot
be treated as success. Request content and sequence are never changed to bypass
a conflict or an unresolved CHF operation.

Update/Release CHF HTTP calls have a five-second timeout and inherit recovery
cancellation while retaining OAuth credentials. Token acquisition uses the
existing shared OAuth path. Initial retains its existing timeout behavior.

## CHF acknowledgement and local completion

The immutable CHF response is copied into the work item before consuming usage.
Only the exact captured prefix is removed, once. Later reports remain queued.
Changing or losing the captured prefix prevents completion instead of dropping
a different batch.

A usage Update remains ChargingUpdateQuotaPending until local delivery completes:

1. Validate response owners against the original request and validate live PFCP
   bindings before changing any quota or timer.
2. Apply response information once to still-live original rules. Retired rules
   and replacement URRs with a reused ID do not receive old credit.
3. Freeze quota values and track each response UPF's delivery independently.
   The binding includes UPF instance, association generation, peer IP and both
   local/remote SEIDs. A different PFCP session is never a retry destination.
4. Build PFCP updates for only those rules; unrelated RULE_UPDATE entries are
   excluded. Confirmed UPFs are skipped on subsequent recovery attempts. Timers
   and validity timestamps are not restarted by those attempts.
5. Clear the pending request and return to Idle after all required delivery is
   confirmed or the original rules have retired.

The shared builder's RULE_CREATE state is a deployment hint, not proof of an
accepted quota update. The work item's per-UPF acknowledgement is authoritative.
An accepted PFCP response's usage reports are preserved through HandleReports.

If a UPF has not entered transport, recovery can continue its delivery without
calling CHF again. Once a PFCP call has started, the existing transaction layer
owns retransmission of its packet and sequence. After timeout, rejection or an
invalid response, this stage retains an unconfirmed result and does not create
another quota-reset transaction. The result needs confirmation/reconciliation;
retirement of the original rules also ends the need to deliver their credit.
Other unsent UPFs can still complete. New Update/Release operations stay blocked
while this resource's quota continuation remains unresolved.

Trigger-only UpdateChargingSession notifications preserve their original ULCL
behavior. ADDITION_OF_UPF can precede PFCP establishment, so recovery completes
that notification without starting a quota-modification transaction or consuming
unreported usage. The ULCL caller continues to own establishment.

## Release and lifecycle boundaries

A Release request records termination intent before testing the send guard.
New updates stop immediately; an existing frozen Update still resumes to its
original completion. The worker then sends Release with a new invocation
sequence and only the remaining unacknowledged reports. A failed Release keeps
and resends its original snapshot. Permanent charging errors or unconfirmed
quota delivery still block settlement and cleanup.

Settlement intent and permission to remove the context are separate. Calling
ReleaseChargingSession alone does not delete the context: UE/N2 completion may
still be outstanding. RemoveSMContextFromAllNF records cleanup permission and
merges notification requirements. AMF release and local release record that
permission even if settlement currently fails; UE release records it only at
its normal completion boundary (or its existing duplicate-session exception).
Association release keeps its existing AMF-resource-release decision.

Once settlement succeeds and removal is permitted, cleanup runs BSF release,
PCF policy deletion, SDM bookkeeping/unsubscription and asynchronous status
notification/local removal. PCF or UDM failure retains the context and retries
with backoff. Clearing SMPolicyID and SdmCounted records completed stages, so
later attempts do not repeat them or decrement another session's count. A 404
from deletion of the targeted PCF policy/UDM subscription means it is already
absent, including when a previous success response was lost. CHF Release 404
remains an error. PCF/UDM cleanup HTTP calls have a five-second timeout and inherit
worker cancellation; token acquisition retains the existing OAuth path.

Only one asynchronous notification/removal is scheduled. Notification needs
can merge while it waits for SMLock. Callback delivery is best effort with a
five-second HTTP timeout; failure does not prevent local removal. Removal uses
the original SMContext.Ref and cannot remove a replacement context's canonical
reference. Completion discards the in-memory continuation.

All retained work, CHF responses and PFCP progress are in memory. No database
round trips are introduced. A process restart loses them; restart recovery is
not supported by this implementation. The service starts one worker after PFCP
initialization, stops new work on shutdown and joins the worker before returning.

## Validation

The charging recovery tests use real local HTTP/2 CHF stubs, real request/rule
builders and an injected PFCP client boundary. They cover frozen retries and
routing, new usage retention, permanent errors, wrong sequences, independent
UPF progress, cached CHF replies, unknown PFCP outcomes, replacement rules and
sessions, unrelated URRs, ULCL notifications and worker cancellation. Release
tests also cover automatic cleanup after settlement retry, an Update blocking
Release, incomplete cleanup stages, lost deletion replies, merged notifications
and preservation of a replacement session's reference and SDM count.
Existing PFCP transport tests exercise transaction retransmission separately.
These checks do not substitute for a running two-UPF end-to-end reproduction.
