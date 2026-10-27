package processor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/wmnsk/go-pfcp/ie"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	pfcp_message "github.com/free5gc/smf/internal/pfcp/message"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

type chargingQuotaTarget struct {
	upfID                 string
	peerIP                string
	upf                   *smf_context.UPF
	generation            uint64
	localSEID, remoteSEID uint64
	rules                 []*smf_context.URR
	values                []smf_context.URR
	sent, done            bool
	uncertain             error
}

// Update the urr by the charging information renewed by chf
func (p *Processor) updateGrantedQuota(
	smContext *smf_context.SMContext, multipleUnitInformation []models.Chf_ConvCharging_MultipleUnitInformation,
) {
	logger.ChargingLog.Debugf("updateGrantedQuota: Received %d MultipleUnitInformation from CHF",
		len(multipleUnitInformation))

	for _, ui := range multipleUnitInformation {
		rg := ui.RatingGroup
		upfId := ui.UPFID
		logger.ChargingLog.Debugf("Processing CHF response: RatingGroup=%d, UpfId=%s", rg, upfId)

		urrs := getUrrsByRg(smContext, upfId, rg)
		if len(urrs) == 0 {
			retired := false
			for _, info := range smContext.ChargingInfo {
				if info.UpfId == upfId && info.RatingGroup == rg {
					retired = true
					break
				}
			}
			if retired {
				logger.ChargingLog.Debugf("Ignore quota for retired RatingGroup[%d], UpfId=%s", rg, upfId)
				continue
			}
			logger.ChargingLog.Errorf("Cannot find URR for RatingGroup[%d], UpfId=%s - Quota will NOT be updated", rg, upfId)
			continue
		}

		p.updateGrantedQuotaForRules(smContext, ui, urrs)
	}
}

func (p *Processor) updateGrantedQuotaForRules(
	smContext *smf_context.SMContext, ui models.Chf_ConvCharging_MultipleUnitInformation, urrs []*smf_context.URR,
) {
	rg := ui.RatingGroup
	upfId := ui.UPFID

	// Update ALL URRs with the same Rating Group
	for _, urr := range urrs {
		logger.ChargingLog.Debugf("Will update URR[%d] with quota from RatingGroup[%d]", urr.URRID, rg)
		trigger := pfcptype.ReportingTrigger{}
		smContext.SetUrrState(upfId, urr.URRID, smf_context.RULE_UPDATE)
		chgInfo := smContext.ChargingInfo[urr.URRID]
		online := chgInfo.ChargingMethod == models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING
		urr.VolumeThresholdIsTotal = online
		if online {
			// An omitted threshold or a final zero grant must disable the old threshold.
			urr.VolumeThreshold = 0
		}

		for _, t := range ui.Triggers {
			switch t.TriggerType {
			case models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT:
				// According to 32.255, the for the trigger "Expirt of datavolume limit" have two reporting level
				// In the Pdu sesion level, the report should be "Immediate report",
				// that is this report should send to CHF immediately
				// In the Rating Group level, the report should be "Defferd report", that is this report should send to CHF
				// when the in the next charging request triggereed
				// by charging trigger that belongs to the type of immediate report

				// TODO: Currently CHF cannot identify the report level since it only knows the rating group,
				// so the both charging level of "Expirt of datavolume limit"
				// will be included in the report, and the report type will be determined by the SMF
				switch chgInfo.ChargingLevel {
				case smf_context.PduSessionCharging:
					if t.TriggerCategory == models.Chf_ConvCharging_TriggerCategory_IMMEDIATE_REPORT {
						smContext.Log.Infof("Add Volume Limit Expiry Timer for Pdu session, it's rating group is [%d]", rg)

						if chgInfo.VolumeLimitExpiryTimer != nil {
							chgInfo.VolumeLimitExpiryTimer.Stop()
							chgInfo.VolumeLimitExpiryTimer = nil
						}

						chgInfo.VolumeLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.VolumeLimit)*time.Second, 1,
							func(expireTimes int32) {
								smContext.SMLock.Lock()
								defer smContext.SMLock.Unlock()
								entry := smContext.UrrTable[ui.UPFID][urr.URRID]
								if entry == nil || entry.RefCount == 0 || entry.State == smf_context.RULE_REMOVE {
									return
								}
								urrList := []*smf_context.URR{urr}
								upf := smf_context.GetUpfById(ui.UPFID)
								if upf != nil {
									p.QueryReport(smContext, upf, urrList, models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT)
									p.ReportUsageAndUpdateQuota(smContext)
								}
							},
							func() {
								smContext.Log.Tracef("Volume Limit Expiry for Pdu session, it's rating group is [%d]", rg)
								chgInfo.VolumeLimitExpiryTimer.Stop()
								chgInfo.VolumeLimitExpiryTimer = nil
							})
					}
				case smf_context.FlowCharging:
					if t.TriggerCategory == models.Chf_ConvCharging_TriggerCategory_DEFERRED_REPORT {
						smContext.Log.Infof("Add Volume Limit Expiry Timer for rating group [%d] ", rg)

						if chgInfo.VolumeLimitExpiryTimer != nil {
							chgInfo.VolumeLimitExpiryTimer.Stop()
							chgInfo.VolumeLimitExpiryTimer = nil
						}

						chgInfo.VolumeLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.VolumeLimit)*time.Second, 1,
							func(expireTimes int32) {
								smContext.SMLock.Lock()
								defer smContext.SMLock.Unlock()
								entry := smContext.UrrTable[ui.UPFID][urr.URRID]
								if entry == nil || entry.RefCount == 0 || entry.State == smf_context.RULE_REMOVE {
									return
								}
								urrList := []*smf_context.URR{urr}
								upf := smf_context.GetUpfById(ui.UPFID)
								if upf != nil {
									p.QueryReport(smContext, upf, urrList, models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT)
								}
							},
							func() {
								smContext.Log.Tracef("Volume Limit Expiry for rating group [%d]", rg)
								chgInfo.VolumeLimitExpiryTimer.Stop()
								chgInfo.VolumeLimitExpiryTimer = nil
							})
					}
				}
			case models.Chf_ConvCharging_TriggerType_MAX_NUMBER_OF_CHANGES_IN_CHARGING_CONDITIONS:
				switch chgInfo.ChargingLevel {
				case smf_context.PduSessionCharging:
					chgInfo.EventLimitExpiryTimer = smf_context.NewTimer(time.Duration(t.EventLimit)*time.Second, 1,
						func(expireTimes int32) {
							smContext.SMLock.Lock()
							defer smContext.SMLock.Unlock()
							entry := smContext.UrrTable[ui.UPFID][urr.URRID]
							if entry == nil || entry.RefCount == 0 || entry.State == smf_context.RULE_REMOVE {
								return
							}
							urrList := []*smf_context.URR{urr}
							upf := smf_context.GetUpfById(ui.UPFID)
							if upf != nil {
								p.QueryReport(smContext, upf, urrList, models.Chf_ConvCharging_TriggerType_VOLUME_LIMIT)
								p.ReportUsageAndUpdateQuota(smContext)
							}
						},
						func() {
							smContext.Log.Tracef("Event Limit Expiry Timer is triggered")
							chgInfo.EventLimitExpiryTimer = nil
						})
				default:
					smContext.Log.Tracef("MAX_NUMBER_OF_CHANGES_IN_CHARGING_CONDITIONS" +
						"should only be applied to PDU session level charging")
				}
			case models.Chf_ConvCharging_TriggerType_QUOTA_THRESHOLD:
				if ui.VolumeQuotaThreshold <= 0 {
					continue
				}
				if online {
					if ui.GrantedUnit == nil || ui.GrantedUnit.TotalVolume <= 0 {
						continue
					}
					// TS 32.290 5.4.2 specifies remaining quota; PFCP expects consumed volume.
					// Both values apply to the total grant, not to each direction separately.
					quota := uint64(ui.GrantedUnit.TotalVolume)
					remaining := uint64(ui.VolumeQuotaThreshold)
					urr.VolumeThreshold = quota - min(quota, remaining)
				} else {
					// Offline charging has no granted quota and retains its usage threshold.
					urr.VolumeThreshold = uint64(ui.VolumeQuotaThreshold)
				}
				trigger.SetVOLTH()
			// The difference between the quota validity time and the volume limit is
			// that the validity time is counted by the UPF, the volume limit is counted by the SMF
			case models.Chf_ConvCharging_TriggerType_VALIDITY_TIME:
				if ui.ValidityTime != 0 {
					urr.ReportingTrigger.SetQUVTI()
					urr.QuotaValidityTime = time.Now().Add(time.Second * time.Duration(ui.ValidityTime))
				}
			case models.Chf_ConvCharging_TriggerType_QUOTA_EXHAUSTED:
				if chgInfo.ChargingMethod == models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING {
					if ui.GrantedUnit != nil {
						trigger.SetVOLQU()
						urr.VolumeQuota = uint64(ui.GrantedUnit.TotalVolume)
						logger.ChargingLog.Debugf("QUOTA_EXHAUSTED: Setting VolumeQuota=%d", urr.VolumeQuota)
					} else {
						// No granted quota, so set the urr.VolumeQuota to 0, upf should stop send traffic
						logger.ChargingLog.Warnf("No granted quota, setting VolumeQuota=0")
						trigger.SetVOLQU()
						urr.VolumeQuota = 0
					}
				}
			}
		}

		urr.ReportingTrigger = trigger
	} // end for each urr
}

func chargingRuleLive(sm *smf_context.SMContext, upfID string, rule *smf_context.URR) bool {
	entry := sm.UrrTable[upfID][rule.URRID]
	return entry != nil && entry.Rule == rule && entry.RefCount > 0 && entry.State != smf_context.RULE_REMOVE
}

func (p *Processor) deliverChargingQuota(
	ctx context.Context, work *chargingWork, response *models.Chf_ConvCharging_ChargingDataResponse,
) error {
	sm := work.sm
	ref, _, err := sm.PendingChargingRequest(smf_context.CHARGING_UPDATE)
	if err != nil {
		return err
	}
	if ref != sm.ChargingDataRef {
		work.retryable = false
		return fmt.Errorf("charging resource changed before quota delivery")
	}
	if !work.prepared {
		// Validate every live original binding before changing any rule or timer.
		for _, info := range response.MultipleUnitInformation {
			owner := chargingOwner{rg: info.RatingGroup, upf: info.UPFID}
			for _, rule := range work.owners[owner] {
				if chargingRuleLive(sm, owner.upf, rule) {
					if err = validateChargingBinding(sm, work.bindings[owner.upf]); err != nil {
						return err
					}
					break
				}
			}
		}
		for _, info := range response.MultipleUnitInformation {
			owner := chargingOwner{rg: info.RatingGroup, upf: info.UPFID}
			var live []*smf_context.URR
			for _, rule := range work.owners[owner] {
				if chargingRuleLive(sm, info.UPFID, rule) {
					live = append(live, rule)
				}
			}
			if len(live) == 0 {
				continue
			}
			p.updateGrantedQuotaForRules(sm, info, live)
			target := work.bindings[info.UPFID]
			for _, rule := range live {
				found := false
				for _, existing := range target.rules {
					if existing == rule {
						found = true
						break
					}
				}
				if !found {
					target.rules = append(target.rules, rule)
				}
			}
		}
		for _, target := range work.bindings {
			for _, rule := range target.rules {
				target.values = append(target.values, *rule)
			}
		}
		work.prepared = true
	}
	keys := make([]string, 0, len(work.bindings))
	for key := range work.bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var failures []error
	for _, key := range keys {
		target := work.bindings[key]
		if target.done || len(target.rules) == 0 {
			continue
		}
		if err = p.deliverChargingTarget(ctx, sm, target); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (p *Processor) deliverChargingTarget(
	ctx context.Context, sm *smf_context.SMContext, target *chargingQuotaTarget,
) error {
	var live []*smf_context.URR
	for i, rule := range target.rules {
		if !chargingRuleLive(sm, target.upfID, rule) {
			continue
		}
		if *rule != target.values[i] {
			return fmt.Errorf("quota rule changed for UPF %s", target.upfID)
		}
		live = append(live, rule)
	}
	if len(live) == 0 {
		target.done = true
		return nil
	}
	// The transaction layer already retransmits the same packet/sequence. A new
	// transaction after an unknown outcome could reset quota already consumed.
	if target.sent {
		return target.uncertain
	}
	if err := validateChargingBinding(sm, target); err != nil {
		return err
	}
	association, finish, err := target.upf.BeginSessionWork()
	if err != nil {
		return err
	}
	defer finish()
	if err = validateChargingBinding(sm, target); err != nil {
		return err
	}
	ip := net.ParseIP(target.peerIP)

	client, ok := p.getActivePFCPClient().(SessionModificationPFCPClient)
	if !ok {
		return fmt.Errorf("PFCP quota client is not configured")
	}
	// Build only this reply's rules. Unrelated RULE_UPDATE entries are excluded.
	for _, rule := range live {
		sm.SetUrrState(target.upfID, rule.URRID, smf_context.RULE_UPDATE)
	}
	request, err := pfcp_message.BuildSessionModificationRequest(ip.String(), target.upfID, sm, nil, nil, nil, nil, live)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithCancel(association)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	if err = ctx.Err(); err != nil {
		return err
	}
	previousState := sm.State()
	sm.SetState(smf_context.PFCPModification)
	defer sm.SetState(previousState)
	target.sent = true
	target.uncertain = fmt.Errorf("quota delivery outcome for UPF %s requires confirmation", target.upfID)
	reply, err := client.SendSessionModificationRequest(
		requestCtx, request, &net.UDPAddr{IP: ip, Port: pfcpPeerPort}, target.localSEID)
	if err != nil {
		target.uncertain = fmt.Errorf("%w: %w", target.uncertain, err)
		return target.uncertain
	}
	if reply == nil || reply.Cause == nil || reply.SEID() != target.localSEID || reply.Sequence() != request.Sequence() {
		return target.uncertain
	}
	cause, causeErr := reply.Cause.Cause()
	if causeErr != nil || cause != ie.CauseRequestAccepted {
		target.uncertain = fmt.Errorf("quota UPF %s returned cause=%d err=%v; confirmation required",
			target.upfID, cause, causeErr)
		return target.uncertain
	}
	target.done = true
	target.uncertain = nil
	if len(reply.UsageReport) != 0 {
		if err = sm.HandleReports(reply.UsageReport, target.upf.NodeID, ""); err != nil {
			logger.ChargingLog.Warnf("Decode quota modification usage reports: %v", err)
		}
	}
	return nil
}

func validateChargingBinding(sm *smf_context.SMContext, target *chargingQuotaTarget) error {
	if target == nil || target.upf == nil {
		return fmt.Errorf("original quota UPF is unavailable")
	}
	if smf_context.GetUpfById(target.upfID) != target.upf {
		return fmt.Errorf("original quota UPF %s was replaced", target.upfID)
	}
	state, generation := target.upf.AssociationStateAndGeneration()
	if state != smf_context.AssociationEstablished || generation != target.generation {
		return fmt.Errorf("quota UPF %s association changed", target.upfID)
	}
	if target.upf.NodeID.ResolveNodeIdToIp().String() != target.peerIP {
		return fmt.Errorf("quota UPF %s address changed", target.upfID)
	}
	session := sm.PFCPContext[target.peerIP]
	if session == nil || session.LocalSEID != target.localSEID || session.RemoteSEID != target.remoteSEID {
		return fmt.Errorf("quota PFCP session changed for UPF %s", target.upfID)
	}
	return nil
}
