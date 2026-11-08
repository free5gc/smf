package context

import (
	"fmt"
	"net"

	"github.com/pkg/errors"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/flowdesc"
)

// SessionRule - A session rule consists of policy information elements
// associated with PDU session.
type SessionRule struct {
	*models.Pcf_SMPolCtrl_SessionRule
	DefQosQFI uint8
}

// NewSessionRule - create session rule from OpenAPI models
func NewSessionRule(model *models.Pcf_SMPolCtrl_SessionRule) *SessionRule {
	if model == nil {
		return nil
	}

	return &SessionRule{
		Pcf_SMPolCtrl_SessionRule: model,
		DefQosQFI:                 1,
	}
}

// TrafficControlData - Traffic control data defines how traffic data flows
// associated with a rule are treated (e.g. blocked, redirected).
type TrafficControlData struct {
	*models.Pcf_SMPolCtrl_TrafficControlData
}

// NewTrafficControlData - create the traffic control data from OpenAPI model
func NewTrafficControlData(model *models.Pcf_SMPolCtrl_TrafficControlData) *TrafficControlData {
	if model == nil {
		return nil
	}

	return &TrafficControlData{
		Pcf_SMPolCtrl_TrafficControlData: model,
	}
}

// PCCRule - Policy and Charging Rule
type PCCRule struct {
	*models.Pcf_SMPolCtrl_PccRule
	QFI uint8
	// related Data
	Datapath *DataPath
}

// NewPCCRule - create PCC rule from OpenAPI models
func NewPCCRule(mPcc *models.Pcf_SMPolCtrl_PccRule) *PCCRule {
	if mPcc == nil {
		return nil
	}

	return &PCCRule{
		Pcf_SMPolCtrl_PccRule: mPcc,
	}
}

func (r *PCCRule) FlowDescription() string {
	if len(r.FlowInfos) > 0 {
		// now 1 pcc rule only maps to 1 FlowInfo
		return r.FlowInfos[0].FlowDescription
	}
	return ""
}

func (r *PCCRule) RefChgDataID() string {
	if len(r.RefChgData) > 0 {
		// now 1 pcc rule only maps to 1 Charging data
		return r.RefChgData[0]
	}
	return ""
}

func (r *PCCRule) RefQosDataID() string {
	if len(r.RefQosData) > 0 {
		// now 1 pcc rule only maps to 1 QoS data
		return r.RefQosData[0]
	}
	return ""
}

func (r *PCCRule) SetQFI(qfi uint8) {
	r.QFI = qfi
}

func (r *PCCRule) RefTcDataID() string {
	if len(r.RefTcData) > 0 {
		// now 1 pcc rule only maps to 1 Traffic Control data
		return r.RefTcData[0]
	}
	return ""
}

func (r *PCCRule) IdentifyChargingLevel() (ChargingLevel, error) {
	dlIPFilterRule, err := flowdesc.Decode(r.FlowDescription())
	if err != nil {
		return 0, err
	}
	// For the PCC rule that are applicable for all datapath,
	// it's charging level will be PDU-based
	if dlIPFilterRule.Src == "any" && dlIPFilterRule.Dst == "assigned" {
		return PduSessionCharging, nil
	} else {
		// For the PCC rule that is applicable for all datapath for a datapath,
		// it's charging level will be flow-based
		return FlowCharging, nil
	}
}

func (r *PCCRule) UpdateDataPathFlowDescription(dlFlowDesc string) error {
	if r.Datapath == nil {
		return fmt.Errorf("pcc[%s]: no data path", r.PccRuleId)
	}

	if dlFlowDesc == "" {
		return fmt.Errorf("pcc[%s]: no flow description", r.PccRuleId)
	}

	ulFlowDesc := dlFlowDesc
	r.Datapath.UpdateFlowDescription(ulFlowDesc, dlFlowDesc) // UL, DL flow description should be same
	return nil
}

func (r *PCCRule) AddDataPathForwardingParameters(c *SMContext,
	tgtRoute *models.RouteToLocation,
) {
	if tgtRoute == nil {
		return
	}

	if r.Datapath == nil {
		logger.CtxLog.Warnf("AddDataPathForwardingParameters pcc[%s]: no data path", r.PccRuleId)
		return
	}

	var routeProf factory.RouteProfile
	routeProfExist := false
	// specify N6 routing information
	if tgtRoute.RouteProfId != "" {
		routeProf, routeProfExist = factory.UERoutingConfig.RouteProf[factory.RouteProfID(tgtRoute.RouteProfId)]
		if !routeProfExist {
			logger.CtxLog.Warnf("Route Profile ID [%s] is not support", tgtRoute.RouteProfId)
			return
		}
	}
	if c.Tunnel.DataPathPool.GetDefaultPath() == nil {
		logger.CtxLog.Infoln("No Default Data Path")
	} else {
		r.Datapath.AddForwardingParameters(routeProf.ForwardingPolicyID,
			c.Tunnel.DataPathPool.GetDefaultPath().FirstDPNode.GetUpLinkPDR().PDI.LocalFTeid.Teid)
	}
}

func (r *PCCRule) AddDataPathForwardingParametersOnDcTunnel(c *SMContext,
	tgtRoute *models.RouteToLocation,
) {
	if tgtRoute == nil {
		return
	}

	if r.Datapath == nil {
		logger.CtxLog.Warnf("AddDataPathForwardingParametersOnDcTunnel pcc[%s]: no data path", r.PccRuleId)
		return
	}

	var routeProf factory.RouteProfile
	routeProfExist := false
	// specify N6 routing information
	if tgtRoute.RouteProfId != "" {
		routeProf, routeProfExist = factory.UERoutingConfig.RouteProf[factory.RouteProfID(tgtRoute.RouteProfId)]
		if !routeProfExist {
			logger.CtxLog.Warnf("Route Profile ID [%s] is not support on DCTunnel", tgtRoute.RouteProfId)
			return
		}
	}

	if c.DCTunnel.DataPathPool.GetDefaultPath() == nil {
		logger.CtxLog.Infoln("No Default Data Path")
	} else {
		r.Datapath.AddForwardingParameters(routeProf.ForwardingPolicyID,
			c.DCTunnel.DataPathPool.GetDefaultPath().FirstDPNode.GetUpLinkPDR().PDI.LocalFTeid.Teid)
	}
}

func (r *PCCRule) BuildNasQoSRule(smCtx *SMContext,
	opCode nasie.QosRuleOpCode,
) (*nasie.QosRule, error) {
	rule := nasie.QosRule{}
	rule.OpCode = opCode
	rule.Precedence = uint8(r.Precedence)
	rule.QFI = r.QFI
	if opCode == nasie.OpCode_DelExistingQosRule ||
		opCode == nasie.OpCode_ModifyWoModifyingPktFilters {
		return &rule, nil
	}
	pfList := make([]nasie.PacketFilter, 0)
	for _, flowInfo := range r.FlowInfos {
		if pfs, err := BuildNASPacketFiltersFromFlowInformation(&flowInfo, smCtx); err != nil {
			logger.CtxLog.Warnf("BuildNasQoSRule: Build packet filter fail: %s\n", err)
			continue
		} else {
			pfList = append(pfList, pfs...)
		}
	}
	rule.PktFilterList = pfList

	return &rule, nil
}

func portRangeToString(p *flowdesc.PortRange) string {
	if p == nil || (p.Start == 0 && p.End == 0) {
		return ""
	}
	if p.Start == p.End {
		return fmt.Sprintf("%d", p.Start)
	}
	return fmt.Sprintf("%d-%d", p.Start, p.End)
}

func createNasPacketFilter(
	pfInfo *models.Pcf_SMPolCtrl_FlowInformation,
	smCtx *SMContext,
	ipFilterRule *flowdesc.IPFilterRule,
	srcP *flowdesc.PortRange,
	dstP *flowdesc.PortRange,
) (*nasie.PacketFilter, error) {
	pf := new(nasie.PacketFilter)

	pfId, errAllocate := smCtx.PacketFilterIDGenerator.Allocate()
	if errAllocate != nil {
		return nil, errAllocate
	}
	pf.Id = uint8(pfId)
	smCtx.PacketFilterIDToNASPFID[pfInfo.PackFiltId] = uint8(pfId)

	switch pfInfo.FlowDirection {
	case models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK:
		pf.Dir = nasie.PFD_Downlink
	case models.Pcf_SMPolCtrl_FlowDirectionRm_UPLINK:
		pf.Dir = nasie.PFD_Uplink
	case models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL:
		pf.Dir = nasie.PFD_BiDir
	}

	// "any"/"assigned" mark the address components as absent (see ie.PacketFilterContents).
	contents := nasie.PacketFilterContents{
		RemoteAddr: "any",
		LocalAddr:  "assigned",
	}

	// FlowLabel, Spi and TosTrafficClass are kept in their hex string form.
	contents.FlowLabel = pfInfo.FlowLabel
	contents.SPI = pfInfo.Spi
	contents.TosTrafficClass = pfInfo.TosTrafficClass

	if ipFilterRule.Dst != "assigned" {
		if _, _, errParseCIDR := net.ParseCIDR(ipFilterRule.Dst); errParseCIDR != nil {
			return nil, fmt.Errorf("parse IP fail: %s", errParseCIDR)
		}
		contents.LocalAddr = ipFilterRule.Dst
	}
	contents.LocalPortRange = portRangeToString(dstP)

	if ipFilterRule.Src != "any" {
		if _, _, errParseCIDR := net.ParseCIDR(ipFilterRule.Src); errParseCIDR != nil {
			return nil, fmt.Errorf("parse IP fail: %s", errParseCIDR)
		}
		contents.RemoteAddr = ipFilterRule.Src
	}
	contents.RemotePortRange = portRangeToString(srcP)

	if ipFilterRule.Proto != flowdesc.ProtocolNumberAny {
		contents.HavePIorNH = true
		contents.PIorNH = ipFilterRule.Proto
	}

	pf.Contents = contents
	return pf, nil
}

func BuildNASPacketFiltersFromFlowInformation(pfInfo *models.Pcf_SMPolCtrl_FlowInformation,
	smCtx *SMContext,
) ([]nasie.PacketFilter, error) {
	var pfList []nasie.PacketFilter

	ipFilterRule := flowdesc.NewIPFilterRule()
	if pfInfo.FlowDescription != "" {
		var err error
		ipFilterRule, err = flowdesc.Decode(pfInfo.FlowDescription)
		if err != nil {
			return nil, fmt.Errorf("parse packet filter content fail: %s", err)
		}
	}

	// TS 24.501 9.11.4.13.4
	srcPLen := len(ipFilterRule.SrcPorts)
	dstPLen := len(ipFilterRule.DstPorts)
	switch {
	case srcPLen > 0 && dstPLen > 0:
		for _, srcP := range ipFilterRule.SrcPorts {
			for _, dstP := range ipFilterRule.DstPorts {
				pf, err := createNasPacketFilter(pfInfo, smCtx, ipFilterRule, &srcP, &dstP)
				if err != nil {
					return nil, errors.Wrap(err, "create packet filter fail")
				}
				pfList = append(pfList, *pf)
			}
		}
	case srcPLen == 0 && dstPLen > 0:
		for _, dstP := range ipFilterRule.DstPorts {
			pf, err := createNasPacketFilter(pfInfo, smCtx, ipFilterRule, nil, &dstP)
			if err != nil {
				return nil, errors.Wrap(err, "create packet filter fail")
			}
			pfList = append(pfList, *pf)
		}
	case srcPLen > 0 && dstPLen == 0:
		for _, srcP := range ipFilterRule.SrcPorts {
			pf, err := createNasPacketFilter(pfInfo, smCtx, ipFilterRule, &srcP, nil)
			if err != nil {
				return nil, errors.Wrap(err, "create packet filter fail")
			}
			pfList = append(pfList, *pf)
		}
	case srcPLen == 0 && dstPLen == 0:
		pf, err := createNasPacketFilter(pfInfo, smCtx, ipFilterRule, nil, nil)
		if err != nil {
			return nil, errors.Wrap(err, "create packet filter fail")
		}
		pfList = append(pfList, *pf)
	default:
		return nil, errors.Errorf("invalid srcPLen(%d) or dstPLen(%d)", srcPLen, dstPLen)
	}

	return pfList, nil
}
