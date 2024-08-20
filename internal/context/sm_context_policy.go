package context

import (
	"fmt"
	"reflect"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
)

// SM Policy related operation

// SelectedSessionRule - return the SMF selected session rule for this SM Context
func (smContext *SMContext) SelectedSessionRule() *SessionRule {
	if smContext.SelectedSessionRuleID == "" {
		return nil
	}
	return smContext.SessionRules[smContext.SelectedSessionRuleID]
}

func AuthDefQosToString(a *models.AuthorizedDefaultQos) string {
	str := "AuthorizedDefaultQos: {"
	str += fmt.Sprintf("Var5qi: %d ", a.Var5qi)
	str += fmt.Sprintf("Arp: %+v ", a.Arp)
	str += fmt.Sprintf("PriorityLevel: %d ", a.PriorityLevel)
	str += "} "
	return str
}

func SessRuleToString(rule *models.SessionRule, prefix string) string {
	str := prefix + "SessionRule {"
	str += fmt.Sprintf("AuthSessAmbr: { %+v } ", rule.AuthSessAmbr)
	str += fmt.Sprint(AuthDefQosToString(rule.AuthDefQos))
	str += fmt.Sprintf("SessRuleId: %s ", rule.SessRuleId)
	str += fmt.Sprintf("RefUmData: %s ", rule.RefUmData)
	str += fmt.Sprintf("RefCondData: %s ", rule.RefCondData)
	str += prefix + "}"
	return str
}

func (smContext *SMContext) ApplySessionRules(
	decision *models.SmPolicyDecision,
) error {
	if decision == nil {
		return fmt.Errorf("SmPolicyDecision is nil")
	}

	for id, r := range decision.SessRules {
		if r == nil {
			smContext.Log.Debugf("Delete SessionRule[%s]", id)
			delete(smContext.SessionRules, id)
		} else {
			if origRule, ok := smContext.SessionRules[id]; ok {
				smContext.Log.Debugf("Modify SessionRule[%s]: %+v", id, r)
				origRule.SessionRule = r
			} else {
				smContext.Log.Debugf("Install SessionRule[%s]: %s", id, SessRuleToString(r, ""))
				smContext.SessionRules[id] = NewSessionRule(r)
			}
		}
	}

	if len(smContext.SessionRules) == 0 {
		return fmt.Errorf("ApplySessionRules: no session rule")
	}

	if smContext.SelectedSessionRuleID == "" ||
		smContext.SessionRules[smContext.SelectedSessionRuleID] == nil {
		// No active session rule, randomly choose a session rule to activate
		for id := range smContext.SessionRules {
			smContext.SelectedSessionRuleID = id
			break
		}
	}

	// TODO: need update default data path if SessionRule changed
	return nil
}

func (smContext *SMContext) AddQosFlow(qfi uint8, qos *models.QosData) {
	qosFlow := NewQoSFlow(qfi, qos)
	if qosFlow != nil {
		smContext.AdditonalQosFlows[qfi] = qosFlow
	}
}

func (smContext *SMContext) RemoveQosFlow(qfi uint8) {
	delete(smContext.AdditonalQosFlows, qfi)
}

// For urr that created for Pdu session level charging, it shall be applied to all data path
func (smContext *SMContext) addPduLevelChargingRuleToFlow(pccRules map[string]*PCCRule) {
	var pduLevelChargingUrrs []*URR

	// First, select charging URRs from pcc rule, which charging level is PDU Session level
	for id, pcc := range pccRules {
		if chargingLevel, err := pcc.IdentifyChargingLevel(); err != nil {
			continue
		} else if chargingLevel == PduSessionCharging {
			pduPcc := pccRules[id]
			pduLevelChargingUrrs = pduPcc.Datapath.GetChargingUrr(smContext)
			break
		}
	}

	for _, flowPcc := range pccRules {
		if chgLevel, err := flowPcc.IdentifyChargingLevel(); err != nil {
			continue
		} else if chgLevel == FlowCharging {
			for node := flowPcc.Datapath.FirstDPNode; node != nil; node = node.Next() {
				if node.IsAnchorUPF() {
					// only the traffic on the PSA UPF will be charged
					if node.UpLinkTunnel != nil && node.UpLinkTunnel.PDR != nil {
						node.UpLinkTunnel.PDR.AppendURRs(pduLevelChargingUrrs)
					}
					if node.DownLinkTunnel != nil && node.DownLinkTunnel.PDR != nil {
						node.DownLinkTunnel.PDR.AppendURRs(pduLevelChargingUrrs)
					}
				}
			}
		}
	}

	defaultPath := smContext.Tunnel.DataPathPool.GetDefaultPath()
	if defaultPath == nil {
		logger.CtxLog.Errorln("No default data path")
		return
	}

	for node := defaultPath.FirstDPNode; node != nil; node = node.Next() {
		if node.IsAnchorUPF() {
			if node.UpLinkTunnel != nil && node.UpLinkTunnel.PDR != nil {
				node.UpLinkTunnel.PDR.AppendURRs(pduLevelChargingUrrs)
			}
			if node.DownLinkTunnel != nil && node.DownLinkTunnel.PDR != nil {
				node.DownLinkTunnel.PDR.AppendURRs(pduLevelChargingUrrs)
			}
		}
	}
}

func (smContext *SMContext) ApplyPccRules(
	decision *models.SmPolicyDecision,
) error {
	if decision == nil {
		return fmt.Errorf("SmPolicyDecision is nil")
	}

	finalPccRules := make(map[string]*PCCRule)
	finalTcDatas := make(map[string]*TrafficControlData)
	finalQosDatas := make(map[string]*models.QosData)
	finalChgDatas := make(map[string]*models.ChargingData)
	// Handle QoSData
	for id, qos := range decision.QosDecs {
		if qos == nil {
			// If QoS Data is nil should remove QFI
			smContext.RemoveQFI(id)
		}
	}

	// Handle PccRules in decision first
	for id, pccModel := range decision.PccRules {
		var srcTcData, tgtTcData *TrafficControlData
		srcPcc := smContext.PCCRules[id]
		if pccModel == nil {
			smContext.Log.Infof("Remove PCCRule[%s]", id)
			if srcPcc == nil {
				smContext.Log.Warnf("PCCRule[%s] not exist", id)
				continue
			}

			srcTcData = smContext.TrafficControlDatas[srcPcc.RefTcDataID()]
			smContext.PreRemoveDataPath(srcPcc.Datapath)
		} else {
			tgtPcc := NewPCCRule(pccModel)

			tgtTcID := tgtPcc.RefTcDataID()
			_, tgtTcData = smContext.getSrcTgtTcData(decision.TraffContDecs, tgtTcID)

			tgtChgID := tgtPcc.RefChgDataID()
			_, tgtChgData := smContext.getSrcTgtChgData(decision.ChgDecs, tgtChgID)

			tgtQosID := tgtPcc.RefQosDataID()
			_, tgtQosData := smContext.getSrcTgtQosData(decision.QosDecs, tgtQosID)
			tgtPcc.SetQFI(smContext.AssignQFI(tgtQosID))

			// Create Data path for targetPccRule
			if err := smContext.CreatePccRuleDataPath(tgtPcc, tgtTcData, tgtQosData, tgtChgData); err != nil {
				return err
			}
			if srcPcc != nil {
				smContext.Log.Infof("Modify PCCRule[%s]", id)
				srcTcData = smContext.TrafficControlDatas[srcPcc.RefTcDataID()]
				smContext.PreRemoveDataPath(srcPcc.Datapath)
			} else {
				smContext.Log.Infof("Install PCCRule[%s]", id)
			}

			if err := applyFlowInfoOrPFD(tgtPcc); err != nil {
				return err
			}
			finalPccRules[id] = tgtPcc
			if tgtTcID != "" {
				finalTcDatas[tgtTcID] = tgtTcData
			}
			if tgtQosID != "" {
				finalQosDatas[tgtQosID] = tgtQosData
			}
		}
		if err := checkUpPathChangeEvt(smContext, srcTcData, tgtTcData); err != nil {
			smContext.Log.Warnf("Check UpPathChgEvent err: %v", err)
		}
		// Remove handled pcc rule
		delete(smContext.PCCRules, id)
	}

	// Handle PccRules not in decision
	for id, pcc := range smContext.PCCRules {
		tcID := pcc.RefTcDataID()
		srcTcData, tgtTcData := smContext.getSrcTgtTcData(decision.TraffContDecs, tcID)

		chgID := pcc.RefChgDataID()
		srcChgData, tgtChgData := smContext.getSrcTgtChgData(decision.ChgDecs, chgID)

		qosID := pcc.RefQosDataID()
		srcQosData, tgtQosData := smContext.getSrcTgtQosData(decision.QosDecs, qosID)

		if !reflect.DeepEqual(srcTcData, tgtTcData) ||
			!reflect.DeepEqual(srcQosData, tgtQosData) ||
			!reflect.DeepEqual(srcChgData, tgtChgData) {
			// Remove old Data path
			smContext.PreRemoveDataPath(pcc.Datapath)
			// Create new Data path
			if err := smContext.CreatePccRuleDataPath(pcc, tgtTcData, tgtQosData, tgtChgData); err != nil {
				return err
			}
			if err := checkUpPathChangeEvt(smContext, srcTcData, tgtTcData); err != nil {
				smContext.Log.Warnf("Check UpPathChgEvent err: %v", err)
			}
		}
		finalPccRules[id] = pcc
		if tcID != "" {
			finalTcDatas[tcID] = tgtTcData
		}
		if qosID != "" {
			finalQosDatas[qosID] = tgtQosData
		}
		if chgID != "" {
			finalChgDatas[chgID] = tgtChgData
		}
	}
	// For PCC rule that is for Pdu session level charging, add the created session rules to all other flow
	// so that all volume in the Pdu session could be recorded and charged for the Pdu session
	smContext.addPduLevelChargingRuleToFlow(finalPccRules)

	smContext.PCCRules = finalPccRules
	smContext.TrafficControlDatas = finalTcDatas
	smContext.QosDatas = finalQosDatas
	smContext.ChargingData = finalChgDatas
	return nil
}

func (smContext *SMContext) getSrcTgtTcData(
	decisionTcDecs map[string]*models.TrafficControlData,
	tcID string,
) (*TrafficControlData, *TrafficControlData) {
	if tcID == "" {
		return nil, nil
	}

	srcTcData := smContext.TrafficControlDatas[tcID]
	tgtTcData := NewTrafficControlData(decisionTcDecs[tcID])
	if tgtTcData == nil {
		// no TcData in decision, use source TcData as target TcData
		tgtTcData = srcTcData
	}
	return srcTcData, tgtTcData
}

func (smContext *SMContext) getSrcTgtChgData(
	decisionChgDecs map[string]*models.ChargingData,
	chgID string,
) (*models.ChargingData, *models.ChargingData) {
	if chgID == "" {
		return nil, nil
	}

	srcChgData := smContext.ChargingData[chgID]
	tgtChgData := decisionChgDecs[chgID]
	if tgtChgData == nil {
		// no TcData in decision, use source TcData as target TcData
		tgtChgData = srcChgData
	}
	return srcChgData, tgtChgData
}

func (smContext *SMContext) getSrcTgtQosData(
	decisionQosDecs map[string]*models.QosData,
	qosID string,
) (*models.QosData, *models.QosData) {
	if qosID == "" {
		return nil, nil
	}

	srcQosData := smContext.QosDatas[qosID]
	tgtQosData := decisionQosDecs[qosID]
	if tgtQosData == nil {
		// no TcData in decision, use source TcData as target TcData
		tgtQosData = srcQosData
	}
	return srcQosData, tgtQosData
}

// Set data path PDR to REMOVE beofre sending PFCP req
func (smContext *SMContext) PreRemoveDataPath(dp *DataPath) {
	if dp == nil {
		return
	}
	smContext.MarkPDRsAsRemove(dp)
	smContext.DataPathToBeRemoved[dp.PathID] = dp
}

// Remove data path after receiving PFCP rsp
func (smContext *SMContext) PostRemoveDataPath() {
	for id, dp := range smContext.DataPathToBeRemoved {
		dp.DeactivateTunnelAndPDR(smContext)
		smContext.Tunnel.RemoveDataPath(id)
		delete(smContext.DataPathToBeRemoved, id)
	}
}

func applyFlowInfoOrPFD(pcc *PCCRule) error {
	appID := pcc.AppId

	if len(pcc.FlowInfos) == 0 && appID == "" {
		return fmt.Errorf("No FlowInfo and AppID")
	}

	// Apply flow description if it presents
	if flowDesc := pcc.FlowDescription(); flowDesc != "" {
		if err := pcc.UpdateDataPathFlowDescription(flowDesc); err != nil {
			return err
		}
		return nil
	}
	logger.CfgLog.Tracef("applyFlowInfoOrPFD %+v", pcc.FlowDescription())

	// Find PFD with AppID if no flow description presents
	// TODO: Get PFD from NEF (not from config)
	var matchedPFD *factory.PfdDataForApp
	for _, pfdDataForApp := range factory.UERoutingConfig.PfdDatas {
		if pfdDataForApp.AppID == appID {
			matchedPFD = pfdDataForApp
			break
		}
	}

	// Workaround: UPF doesn't support PFD. Get FlowDescription from PFD.
	// TODO: Send PFD to UPF
	if matchedPFD == nil ||
		len(matchedPFD.Pfds) == 0 ||
		len(matchedPFD.Pfds[0].FlowDescriptions) == 0 {
		return fmt.Errorf("No PFD matched for AppID [%s]", appID)
	}
	if err := pcc.UpdateDataPathFlowDescription(
		matchedPFD.Pfds[0].FlowDescriptions[0]); err != nil {
		return err
	}
	return nil
}

func checkUpPathChangeEvt(smContext *SMContext,
	srcTcData, tgtTcData *TrafficControlData,
) error {
	var srcRoute, tgtRoute models.RouteToLocation
	var upPathChgEvt *models.UpPathChgEvent

	if srcTcData == nil && tgtTcData == nil {
		smContext.Log.Traceln("No srcTcData and tgtTcData. Nothing to do")
		return nil
	}

	// Set reference to traffic control data
	if srcTcData != nil {
		if len(srcTcData.RouteToLocs) == 0 {
			return fmt.Errorf("No RouteToLocs in srcTcData")
		}
		// TODO: Fix always choosing the first RouteToLocs as source Route
		srcRoute = srcTcData.RouteToLocs[0]
		// If no target TcData, the default UpPathChgEvent will be the one in source TcData
		upPathChgEvt = srcTcData.UpPathChgEvent
	} else {
		// No source TcData, get default route
		srcRoute = models.RouteToLocation{
			Dnai: "",
		}
	}

	if tgtTcData != nil {
		if len(tgtTcData.RouteToLocs) == 0 {
			return fmt.Errorf("No RouteToLocs in tgtTcData")
		}
		// TODO: Fix always choosing the first RouteToLocs as target Route
		tgtRoute = tgtTcData.RouteToLocs[0]
		// If target TcData is available, change UpPathChgEvent to the one in target TcData
		upPathChgEvt = tgtTcData.UpPathChgEvent
	} else {
		// No target TcData in decision, roll back to the default route
		tgtRoute = models.RouteToLocation{
			Dnai: "",
		}
	}

	if !reflect.DeepEqual(srcRoute, tgtRoute) {
		smContext.BuildUpPathChgEventExposureNotification(upPathChgEvt, &srcRoute, &tgtRoute)
	}

	return nil
}
