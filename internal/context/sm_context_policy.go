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
func (c *SMContext) SelectedSessionRule() *SessionRule {
	if c.SelectedSessionRuleID == "" {
		return nil
	}
	return c.SessionRules[c.SelectedSessionRuleID]
}

func (c *SMContext) ApplySessionRules(
	decision *models.SmPolicyDecision,
) error {
	if decision == nil {
		return fmt.Errorf("SmPolicyDecision is nil")
	}

	for id, r := range decision.SessRules {
		if r == nil {
			c.Log.Debugf("Delete SessionRule[%s]", id)
			delete(c.SessionRules, id)
		} else {
			if origRule, ok := c.SessionRules[id]; ok {
				c.Log.Debugf("Modify SessionRule[%s]: %+v", id, r)
				origRule.SessionRule = r
			} else {
				c.Log.Debugf("Install SessionRule[%s]: %+v", id, r)
				c.SessionRules[id] = NewSessionRule(r)
			}
		}
	}

	if len(c.SessionRules) == 0 {
		return fmt.Errorf("ApplySessionRules: no session rule")
	}

	if c.SelectedSessionRuleID == "" ||
		c.SessionRules[c.SelectedSessionRuleID] == nil {
		// No active session rule, randomly choose a session rule to activate
		for id := range c.SessionRules {
			c.SelectedSessionRuleID = id
			break
		}
	}

	// TODO: need update default data path if SessionRule changed
	return nil
}

func (c *SMContext) AddQosFlow(qfi uint8, qos *models.QosData) {
	qosFlow := NewQoSFlow(qfi, qos)
	if qosFlow != nil {
		c.AdditonalQosFlows[qfi] = qosFlow
	}
}

func (c *SMContext) RemoveQosFlow(qfi uint8) {
	delete(c.AdditonalQosFlows, qfi)
}

func (c *SMContext) ApplyPccRules(decision *models.SmPolicyDecision) error {
	if decision == nil {
		return fmt.Errorf("SmPolicyDecision is nil")
	}

	finalPccRules := make(map[string]*PCCRule)
	finalTcDatas := make(map[string]*TrafficControlData)
	finalQosDatas := make(map[string]*models.QosData)
	finalChgDatas := make(map[string]*models.ChargingData)

	// Handle QoSData Deletion
	for id, qos := range decision.QosDecs {
		if qos == nil {
			c.RemoveQFI(id)
		}
	}

	pduRulesToProcess := make(map[string]*PCCRule)
	flowRulesToProcess := make(map[string]*PCCRule)

	// Helper function: queue rules to be processed in Pass 1 or Pass 2
	queueRule := func(id string, pcc *PCCRule) {
		if chgLevel, _ := pcc.IdentifyChargingLevel(); chgLevel == PduSessionCharging {
			pduRulesToProcess[id] = pcc
		} else {
			flowRulesToProcess[id] = pcc
		}
	}

	// Shared logic for processing each rule in Pass 1 and Pass 2
	processRule := func(id string, tgtPcc *PCCRule, pduChgDatas []*models.ChargingData) error {
		tgtTcID := tgtPcc.RefTcDataID()
		_, tgtTcData := c.getSrcTgtTcData(decision.TraffContDecs, tgtTcID)
		tgtChgID := tgtPcc.RefChgDataID()
		_, tgtChgData := c.getSrcTgtChgData(decision.ChgDecs, tgtChgID)
		tgtQosID := tgtPcc.RefQosDataID()
		_, tgtQosData := c.getSrcTgtQosData(decision.QosDecs, tgtQosID)

		if tgtQosID != "" {
			tgtPcc.SetQFI(c.AssignQFI(tgtQosID))
		}

		var srcTcData *TrafficControlData
		if srcPcc := c.PCCRules[id]; srcPcc != nil {
			c.Log.Infof("Modify PCCRule[%s]", id)
			srcTcData = c.TrafficControlDatas[srcPcc.RefTcDataID()]
			c.PreRemoveDataPath(srcPcc.Datapath)
		} else {
			c.Log.Infof("Install PCCRule[%s]", id)
		}

		// the main difference between Pass 1 and Pass 2: whether pduUrrs is passed in
		if err := c.CreatePccRuleDataPath(tgtPcc, tgtTcData, tgtQosData, tgtChgData, pduChgDatas); err != nil {
			return err
		}
		if err := applyFlowInfoOrPFD(tgtPcc); err != nil {
			return err
		}
		if err := checkUpPathChangeEvt(c, srcTcData, tgtTcData); err != nil {
			c.Log.Warnf("Check UpPathChgEvent err: %v", err)
		}

		finalPccRules[id] = tgtPcc
		if tgtTcID != "" {
			finalTcDatas[tgtTcID] = tgtTcData
		}
		if tgtQosID != "" {
			finalQosDatas[tgtQosID] = tgtQosData
		}
		if tgtChgID != "" {
			finalChgDatas[tgtChgID] = tgtChgData
		}
		return nil
	}

	// ==========================================================
	// Level 1：classifier - Find out which rules are new/modified/deleted
	// ==========================================================

	// 1.1 Handle Removed PccRule (PccMode == nil)
	for id, pccModel := range decision.PccRules {
		if pccModel == nil {
			c.Log.Infof("Remove PCCRule[%s]", id)
			if srcPcc := c.PCCRules[id]; srcPcc != nil {
				c.PreRemoveDataPath(srcPcc.Datapath)
				srcTcData := c.TrafficControlDatas[srcPcc.RefTcDataID()]
				_ = checkUpPathChangeEvt(c, srcTcData, nil)
			} else {
				c.Log.Warnf("PCCRule[%s] not exist", id)
			}
			delete(c.PCCRules, id)
			continue
		}
		queueRule(id, NewPCCRule(pccModel))
	}

	// 1.2 Scan the old rules find out which rules are modified
	for id, pcc := range c.PCCRules {
		if _, ok := decision.PccRules[id]; ok {
			continue // already handled in 1.1
		}

		tcID := pcc.RefTcDataID()
		srcTcData, tgtTcData := c.getSrcTgtTcData(decision.TraffContDecs, tcID)
		chgID := pcc.RefChgDataID()
		srcChgData, tgtChgData := c.getSrcTgtChgData(decision.ChgDecs, chgID)
		qosID := pcc.RefQosDataID()
		srcQosData, tgtQosData := c.getSrcTgtQosData(decision.QosDecs, qosID)

		if !reflect.DeepEqual(srcTcData, tgtTcData) ||
			!reflect.DeepEqual(srcQosData, tgtQosData) ||
			!reflect.DeepEqual(srcChgData, tgtChgData) {

			queueRule(id, pcc) // rule is changed, so it needs to be re-created with new data
		} else {
			// rule is not changed, keep using the old one
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
	}

	// ==========================================================
	// Level 2：sequential execution of Two-Pass injection
	// ==========================================================

	// Pass 1: Pdu Session level charging rules (pduLevelUrrs = nil)
	for id, pcc := range pduRulesToProcess {
		if err := processRule(id, pcc, nil); err != nil {
			return err
		}
	}

	// Collect PDU-level charging data from rules created in Pass 1
	var pduChgDatas []*models.ChargingData
	for _, pcc := range finalPccRules {
		if chgLevel, _ := pcc.IdentifyChargingLevel(); chgLevel == PduSessionCharging && pcc.Datapath != nil {
			chgID := pcc.RefChgDataID()
			if data, ok := finalChgDatas[chgID]; ok {
				pduChgDatas = append(pduChgDatas, data)
			}
		}
	}

	// Pass 2: Flow level charging rules, which need to apply PDU-level charging data to the data path
	for id, pcc := range flowRulesToProcess {
		if err := processRule(id, pcc, pduChgDatas); err != nil {
			return err
		}
	}

	c.PCCRules = finalPccRules
	c.TrafficControlDatas = finalTcDatas
	c.QosDatas = finalQosDatas
	c.ChargingData = finalChgDatas
	return nil
}

func (c *SMContext) ApplyDcPccRulesOnDcTunnel() error {
	if c.DCTunnel == nil {
		c.Log.Errorf("DCTunnel is nil")
		return fmt.Errorf("DCTunnel is nil")
	}

	for id, pcc := range c.PCCRules {
		if pcc == nil {
			continue
		}

		newPcc := &PCCRule{
			PccRule: &models.PccRule{
				FlowInfos:       make([]models.FlowInformation, len(pcc.FlowInfos)),
				AppId:           pcc.AppId,
				AppDescriptor:   pcc.AppDescriptor,
				ContVer:         pcc.ContVer,
				PccRuleId:       pcc.PccRuleId,
				Precedence:      pcc.Precedence,
				AfSigProtocol:   pcc.AfSigProtocol,
				AppReloc:        pcc.AppReloc,
				EasRedisInd:     pcc.EasRedisInd,
				RefQosData:      make([]string, len(pcc.RefQosData)),
				RefTcData:       make([]string, len(pcc.RefTcData)),
				RefChgData:      make([]string, len(pcc.RefChgData)),
				RefChgN3gData:   make([]string, len(pcc.RefChgN3gData)),
				RefUmData:       make([]string, len(pcc.RefUmData)),
				RefUmN3gData:    make([]string, len(pcc.RefUmN3gData)),
				RefCondData:     pcc.RefCondData,
				RefQosMon:       make([]string, len(pcc.RefQosMon)),
				AddrPreserInd:   pcc.AddrPreserInd,
				TscaiTimeDom:    pcc.TscaiTimeDom,
				DisUeNotif:      pcc.DisUeNotif,
				PackFiltAllPrec: pcc.PackFiltAllPrec,
				RefAltQosParams: make([]string, len(pcc.RefAltQosParams)),
			},
			QFI:      pcc.QFI,
			Datapath: nil,
		}

		if pcc.TscaiInputDl != nil {
			newPcc.TscaiInputDl = &models.TscaiInputContainer{
				Periodicity:      pcc.TscaiInputDl.Periodicity,
				BurstArrivalTime: pcc.TscaiInputDl.BurstArrivalTime,
				SurTimeInNumMsg:  pcc.TscaiInputDl.SurTimeInNumMsg,
				SurTimeInTime:    pcc.TscaiInputDl.SurTimeInTime,
			}
		}
		if pcc.TscaiInputUl != nil {
			newPcc.TscaiInputUl = &models.TscaiInputContainer{
				Periodicity:      pcc.TscaiInputUl.Periodicity,
				BurstArrivalTime: pcc.TscaiInputUl.BurstArrivalTime,
				SurTimeInNumMsg:  pcc.TscaiInputUl.SurTimeInNumMsg,
				SurTimeInTime:    pcc.TscaiInputUl.SurTimeInTime,
			}
		}
		copy(newPcc.FlowInfos, pcc.FlowInfos)
		copy(newPcc.RefQosData, pcc.RefQosData)
		copy(newPcc.RefTcData, pcc.RefTcData)
		copy(newPcc.RefChgData, pcc.RefChgData)
		copy(newPcc.RefChgN3gData, pcc.RefChgN3gData)
		copy(newPcc.RefUmData, pcc.RefUmData)
		copy(newPcc.RefUmN3gData, pcc.RefUmN3gData)
		copy(newPcc.RefQosMon, pcc.RefQosMon)
		copy(newPcc.RefAltQosParams, pcc.RefAltQosParams)

		if pcc.DdNotifCtrl != nil {
			newPcc.DdNotifCtrl = &models.DownlinkDataNotificationControl{
				NotifCtrlInds: make([]models.NotificationControlIndication, len(pcc.DdNotifCtrl.NotifCtrlInds)),
				TypesOfNotif:  make([]models.DlDataDeliveryStatus, len(pcc.DdNotifCtrl.TypesOfNotif)),
			}
			copy(newPcc.DdNotifCtrl.NotifCtrlInds, pcc.DdNotifCtrl.NotifCtrlInds)
			copy(newPcc.DdNotifCtrl.TypesOfNotif, pcc.DdNotifCtrl.TypesOfNotif)
		}
		if pcc.DdNotifCtrl2 != nil {
			newPcc.DdNotifCtrl2 = &models.DownlinkDataNotificationControlRm{
				NotifCtrlInds: make([]models.NotificationControlIndication, len(pcc.DdNotifCtrl2.NotifCtrlInds)),
				TypesOfNotif:  make([]models.DlDataDeliveryStatus, len(pcc.DdNotifCtrl2.TypesOfNotif)),
			}
			copy(newPcc.DdNotifCtrl2.NotifCtrlInds, pcc.DdNotifCtrl2.NotifCtrlInds)
			copy(newPcc.DdNotifCtrl2.TypesOfNotif, pcc.DdNotifCtrl2.TypesOfNotif)
		}

		c.DCPCCRules[id] = newPcc
	}
	var pduChgDatas []*models.ChargingData

	// ==========================================================
	// Pass 1: Pdu Session level charging rules of DC tunnel (pduLevelUrrs = nil)
	// ==========================================================
	for id, pcc := range c.DCPCCRules {
		if pcc == nil {
			continue
		}

		chgLevel, _ := pcc.IdentifyChargingLevel()
		if chgLevel == PduSessionCharging {
			tcID := pcc.RefTcDataID()
			tcData := c.TrafficControlDatas[tcID]
			chgID := pcc.RefChgDataID()
			chgData := c.ChargingData[chgID]
			qosID := pcc.RefQosDataID()
			qosData := c.QosDatas[qosID]

			if pcc.Datapath != nil {
				c.PreRemoveDataPath(pcc.Datapath)
			}

			if err := c.CreateDcPccRuleDataPathOnDcTunnel(pcc, tcData, qosData, chgData, nil); err != nil {
				c.Log.Errorf("CreateDcPccRuleDataPathOnDcTunnel (PDU Level) for PCCRule[%s] failed: %v", id, err)
				continue
			}
			if err := applyFlowInfoOrPFD(pcc); err != nil {
				c.Log.Errorf("applyFlowInfoOrPFD for PCCRule[%s] failed: %v", id, err)
				continue
			}

			c.Log.Infof("Applied PDU Level PCCRule[%s] to DCTunnel", id)

			if chgData != nil {
				pduChgDatas = append(pduChgDatas, chgData)
			}
		}
	}

	// ==========================================================
	// Pass 2 - Flow level charging rules of DC tunnel
	// ==========================================================
	for id, pcc := range c.DCPCCRules {
		if pcc == nil {
			continue
		}

		chgLevel, _ := pcc.IdentifyChargingLevel()
		if chgLevel != PduSessionCharging {
			tcID := pcc.RefTcDataID()
			tcData := c.TrafficControlDatas[tcID]
			chgID := pcc.RefChgDataID()
			chgData := c.ChargingData[chgID]
			qosID := pcc.RefQosDataID()
			qosData := c.QosDatas[qosID]

			if pcc.Datapath != nil {
				c.PreRemoveDataPath(pcc.Datapath)
			}

			// main difference between Pass 1 and Pass 2: whether pduChgDatas is passed in
			if err := c.CreateDcPccRuleDataPathOnDcTunnel(pcc, tcData, qosData, chgData, pduChgDatas); err != nil {
				c.Log.Errorf("CreateDcPccRuleDataPathOnDcTunnel (Flow Level) for PCCRule[%s] failed: %v", id, err)
				continue
			}
			if err := applyFlowInfoOrPFD(pcc); err != nil {
				c.Log.Errorf("applyFlowInfoOrPFD for PCCRule[%s] failed: %v", id, err)
				continue
			}

			c.Log.Infof("Applied Flow Level PCCRule[%s] to DCTunnel", id)
		}
	}

	return nil
}

func (c *SMContext) getSrcTgtTcData(
	decisionTcDecs map[string]*models.TrafficControlData,
	tcID string,
) (*TrafficControlData, *TrafficControlData) {
	if tcID == "" {
		return nil, nil
	}

	srcTcData := c.TrafficControlDatas[tcID]
	tgtTcData := NewTrafficControlData(decisionTcDecs[tcID])
	if tgtTcData == nil {
		// no TcData in decision, use source TcData as target TcData
		tgtTcData = srcTcData
	}
	return srcTcData, tgtTcData
}

func (c *SMContext) getSrcTgtChgData(
	decisionChgDecs map[string]*models.ChargingData,
	chgID string,
) (*models.ChargingData, *models.ChargingData) {
	if chgID == "" {
		return nil, nil
	}

	srcChgData := c.ChargingData[chgID]
	tgtChgData := decisionChgDecs[chgID]
	if tgtChgData == nil {
		// no TcData in decision, use source TcData as target TcData
		tgtChgData = srcChgData
	}
	return srcChgData, tgtChgData
}

func (c *SMContext) getSrcTgtQosData(
	decisionQosDecs map[string]*models.QosData,
	qosID string,
) (*models.QosData, *models.QosData) {
	if qosID == "" {
		return nil, nil
	}

	srcQosData := c.QosDatas[qosID]
	tgtQosData := decisionQosDecs[qosID]
	if tgtQosData == nil {
		// no TcData in decision, use source TcData as target TcData
		tgtQosData = srcQosData
	}
	return srcQosData, tgtQosData
}

// Set data path PDR to REMOVE beofre sending PFCP req
func (c *SMContext) PreRemoveDataPath(dp *DataPath) {
	if dp == nil {
		return
	}
	dp.RemovePDR()
	c.DataPathToBeRemoved[dp.PathID] = dp
}

// Remove data path after receiving PFCP rsp
func (c *SMContext) PostRemoveDataPath() {
	for id, dp := range c.DataPathToBeRemoved {
		dp.DeactivateTunnelAndPDR(c)
		c.Tunnel.RemoveDataPath(id)
		delete(c.DataPathToBeRemoved, id)
	}
}

func applyFlowInfoOrPFD(pcc *PCCRule) error {
	appID := pcc.AppId

	if len(pcc.FlowInfos) == 0 && appID == "" {
		return fmt.Errorf("no FlowInfo and AppID")
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
		return fmt.Errorf("no PFD matched for AppID [%s]", appID)
	}
	if err := pcc.UpdateDataPathFlowDescription(
		matchedPFD.Pfds[0].FlowDescriptions[0]); err != nil {
		return err
	}
	return nil
}

func checkUpPathChangeEvt(c *SMContext,
	srcTcData, tgtTcData *TrafficControlData,
) error {
	var srcRoute, tgtRoute models.RouteToLocation
	var upPathChgEvt *models.UpPathChgEvent

	if srcTcData == nil && tgtTcData == nil {
		c.Log.Infof("No srcTcData and tgtTcData. Nothing to do")
		return nil
	}

	// Set reference to traffic control data
	if srcTcData != nil {
		if len(srcTcData.RouteToLocs) == 0 {
			return fmt.Errorf("no RouteToLocs in srcTcData")
		}
		// TODO: Fix always choosing the first RouteToLocs as source Route
		srcRoute = *srcTcData.RouteToLocs[0]
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
			return fmt.Errorf("no RouteToLocs in tgtTcData")
		}
		// TODO: Fix always choosing the first RouteToLocs as target Route
		tgtRoute = *tgtTcData.RouteToLocs[0]
		// If target TcData is available, change UpPathChgEvent to the one in target TcData
		upPathChgEvt = tgtTcData.UpPathChgEvent
	} else {
		// No target TcData in decision, roll back to the default route
		tgtRoute = models.RouteToLocation{
			Dnai: "",
		}
	}

	if !reflect.DeepEqual(srcRoute, tgtRoute) {
		c.BuildUpPathChgEventExposureNotification(upPathChgEvt, &srcRoute, &tgtRoute)
	}

	return nil
}
