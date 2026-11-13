# OAuth and notification operations

TS 29.500 V17.15.0 (Release 17), clause 6.7.3 excludes the Notify operation of
Subscribe/Notify semantics from OAuth access-token use. The operation semantics,
including implicit subscriptions, determine this exception; the callback URL does
not. Subscribe/unsubscribe and request/response operations retain their normal
OAuth policy.

| SMF path | Operation and specification | OAuth token |
| --- | --- | --- |
| Incoming `/nsmf-callback/v1/sm-policies/:smContextRef/update` | Npcf_SMPolicyControl_UpdateNotify: TS 23.502 V17.12.0 table 5.2.5.1-1; TS 29.512 V17.18.0 clauses 4.2.3.2 and 5.5.2 | Not used |
| Incoming `/nsmf-callback/v1/sm-policies/:smContextRef/terminate` | Policy-association termination notification under UpdateNotify: TS 29.512 V17.18.0 clauses 4.2.3.1, 4.2.3.3 and 5.5.3 | Not used; handler remains unimplemented (501) |
| Incoming `/nsmf-callback/v1/:notifyUri` | Nchf_ConvergedCharging_Notify: TS 32.290 V17.7.0 table 6.2.1-1 and clauses 6.2.2/6.2.5; session creation implicitly subscribes the SMF. TS 32.291 V17.12.0 clause 6.1.5 specifies the callback | Not used |
| Outgoing `SendSMContextStatusNotification` | Notify SM Context Status: TS 29.502 V17.12.0 table 5.2.1-1; implicit subscription described explicitly in TS 29.501 V17.7.0 clause 4.6.2.2.1 | Neither requested nor sent |
| Outgoing `SendUpPathChgEventExposureNotification` | Nsmf_EventExposure_Notify: TS 29.508 V17.17.0 table 4.2.1-1 and clause 4.2.2 | Neither requested nor sent |
| `notifyAMFOfDownlinkData` / `N1N2MessageTransfer` | Namf_Communication_N1N2MessageTransfer: TS 23.502 V17.12.0 table 5.2.2.1-1 classifies it as request/response, despite the local helper's name | Retained (`namf-comm`, target AMF instance) |
| Event Exposure subscription CRUD / Nupf subscription create and delete | Subscription management, not Notify delivery | Retained |

`NotifyPCFBindingRelease` currently logs and clears a local binding ID; it sends
no HTTP notification. UPF Event Exposure subscription creation forwards the
subscriber's notification URI to the UPF, rather than adding a receiving SMF
callback route.

Incoming notification routes are explicitly marked by operation semantics.
Other callback routes default to requiring authorization. Outgoing Notify calls
use a context without OAuth credentials. The request/response operations caused
by processing a notification (for example, a subsequent charging update) retain
their own OAuth policy.

Transport protection and application validation of the notification are separate
from this OAuth exception. These focused tests do not establish complete 3GPP
conformance or implement the existing policy-termination stub.

## Official specifications

- [TS 29.500 V17.15.0](https://www.etsi.org/deliver/etsi_ts/129500_129599/129500/17.15.00_60/ts_129500v171500p.pdf)
- [TS 29.501 V17.7.0](https://www.etsi.org/deliver/etsi_ts/129500_129599/129501/17.07.00_60/ts_129501v170700p.pdf)
- [TS 23.502 V17.12.0](https://www.etsi.org/deliver/etsi_ts/123500_123599/123502/17.12.00_60/ts_123502v171200p.pdf)
- [TS 29.502 V17.12.0](https://www.etsi.org/deliver/etsi_ts/129500_129599/129502/17.12.00_60/ts_129502v171200p.pdf)
- [TS 29.508 V17.17.0](https://www.etsi.org/deliver/etsi_ts/129500_129599/129508/17.17.00_60/ts_129508v171700p.pdf)
- [TS 29.512 V17.18.0](https://www.etsi.org/deliver/etsi_ts/129500_129599/129512/17.18.00_60/ts_129512v171800p.pdf)
- [TS 32.290 V17.7.0](https://www.etsi.org/deliver/etsi_ts/132200_132299/132290/17.07.00_60/ts_132290v170700p.pdf)
- [TS 32.291 V17.12.0](https://www.etsi.org/deliver/etsi_ts/132200_132299/132291/17.12.00_60/ts_132291v171200p.pdf)
