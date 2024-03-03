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

// For urr that created for Pdu session level charging, it shall be applied to all data path
func (c *SMContext) addPduLevelChargingRuleToFlow(pccRules map[string]*PCCRule) {
	var pduLevelChargingUrrs []*URR

	// First, select charging URRs from pcc rule, which charging level is PDU Session level
	for id, pcc := range pccRules {
		if chargingLevel, err := pcc.IdentifyChargingLevel(); err != nil {
			continue
		} else if chargingLevel == PduSessionCharging {
			pduPcc := pccRules[id]
			pduLevelChargingUrrs = pduPcc.Datapath.GetChargingUrr(c)
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

	defaultPath := c.Tunnel.DataPathPool.GetDefaultPath()
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

func (c *SMContext) ApplyPccRules(
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
			c.RemoveQFI(id)
		}
	}

	// Handle PccRules in decision first
	for id, pccModel := range decision.PccRules {
		var srcTcData, tgtTcData *TrafficControlData
		srcPcc := c.PCCRules[id]
		if pccModel == nil {
			c.Log.Infof("Remove PCCRule[%s]", id)
			if srcPcc == nil {
				c.Log.Warnf("PCCRule[%s] not exist", id)
				continue
			}

			srcTcData = c.TrafficControlDatas[srcPcc.RefTcDataID()]
			c.PreRemoveDataPath(srcPcc.Datapath)
		} else {
			tgtPcc := NewPCCRule(pccModel)

			tgtTcID := tgtPcc.RefTcDataID()
			_, tgtTcData = c.getSrcTgtTcData(decision.TraffContDecs, tgtTcID)

			tgtChgID := tgtPcc.RefChgDataID()
			_, tgtChgData := c.getSrcTgtChgData(decision.ChgDecs, tgtChgID)

			tgtQosID := tgtPcc.RefQosDataID()
			_, tgtQosData := c.getSrcTgtQosData(decision.QosDecs, tgtQosID)
			tgtPcc.SetQFI(c.AssignQFI(tgtQosID))

			// Create Data path for targetPccRule
			if err := c.CreatePccRuleDataPath(tgtPcc, tgtTcData, tgtQosData, tgtChgData); err != nil {
				return err
			}
			if srcPcc != nil {
				c.Log.Infof("Modify PCCRule[%s]", id)
				srcTcData = c.TrafficControlDatas[srcPcc.RefTcDataID()]
				c.PreRemoveDataPath(srcPcc.Datapath)
			} else {
				c.Log.Infof("Install PCCRule[%s]", id)
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
		if err := checkUpPathChangeEvt(c, srcTcData, tgtTcData); err != nil {
			c.Log.Warnf("Check UpPathChgEvent err: %v", err)
		}
		// Remove handled pcc rule
		delete(c.PCCRules, id)
	}

	// Handle PccRules not in decision
	for id, pcc := range c.PCCRules {
		tcID := pcc.RefTcDataID()
		srcTcData, tgtTcData := c.getSrcTgtTcData(decision.TraffContDecs, tcID)

		chgID := pcc.RefChgDataID()
		srcChgData, tgtChgData := c.getSrcTgtChgData(decision.ChgDecs, chgID)

		qosID := pcc.RefQosDataID()
		srcQosData, tgtQosData := c.getSrcTgtQosData(decision.QosDecs, qosID)

		if !reflect.DeepEqual(srcTcData, tgtTcData) ||
			!reflect.DeepEqual(srcQosData, tgtQosData) ||
			!reflect.DeepEqual(srcChgData, tgtChgData) {
			// Remove old Data path
			c.PreRemoveDataPath(pcc.Datapath)
			// Create new Data path
			if err := c.CreatePccRuleDataPath(pcc, tgtTcData, tgtQosData, tgtChgData); err != nil {
				return err
			}
			if err := checkUpPathChangeEvt(c, srcTcData, tgtTcData); err != nil {
				c.Log.Warnf("Check UpPathChgEvent err: %v", err)
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
	c.addPduLevelChargingRuleToFlow(finalPccRules)

	c.PCCRules = finalPccRules
	c.TrafficControlDatas = finalTcDatas
	c.QosDatas = finalQosDatas
	c.ChargingData = finalChgDatas
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
		return fmt.Errorf("No FlowInfo and AppID")
	}

	// Apply flow description if it presents
	if flowDesc := pcc.FlowDescription(); flowDesc != "" {
		if err := pcc.UpdateDataPathFlowDescription(flowDesc); err != nil {
			return err
		}
		return nil
	}

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
		c.BuildUpPathChgEventExposureNotification(upPathChgEvt, &srcRoute, &tgtRoute)
	}

	return nil
}
