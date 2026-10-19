package context

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/util/flowdesc"
)

const ulclAssignedAddress = "assigned"

// PathQoSBinding couples a classifier with its existing QoS authorization.
// An empty FlowInfos uses the caller's PDR template (normal PCC activation).
type PathQoSBinding struct {
	QFI        uint8
	QosData    *models.Pcf_SMPolCtrl_QosData
	FlowInfos  []models.Pcf_SMPolCtrl_FlowInformation
	Precedence uint32
	source     *PCCRule
}

// ULCLQoSBindings intersects the static route with the current PCC policy.
// Ordered PCC classifiers precede the default fallback; existing QFIs are reused.
func (p *DataPath) ULCLQoSBindings(c *SMContext) ([]PathQoSBinding, error) {
	session := c.SelectedSessionRule()
	if session == nil || session.DefQosQFI == 0 || session.DefQosQFI > 63 {
		return nil, fmt.Errorf("static ULCL route has no valid default QoS flow")
	}
	route := flowdesc.NewIPFilterRule()
	route.Src, route.Dst = p.Destination.DestinationIP, c.PDUAddress.String()
	if route.Src != "any" {
		if _, prefixErr := netip.ParsePrefix(route.Src); prefixErr != nil {
			if _, addrErr := netip.ParseAddr(route.Src); addrErr != nil {
				return nil, fmt.Errorf("invalid static route address %q", route.Src)
			}
		}
	}
	if p.Destination.DestinationPort != "" {
		ports, err := flowdesc.ParsePorts(p.Destination.DestinationPort)
		if err != nil {
			return nil, err
		}
		route.SrcPorts = ports
	}
	description, err := flowdesc.Encode(route)
	if err != nil {
		return nil, err
	}
	filter := models.Pcf_SMPolCtrl_FlowInformation{
		FlowDescription: description, FlowDirection: models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL,
	}
	if _, err = strictULCLFilter(description); err != nil {
		return nil, err
	}
	var rules []*PCCRule
	for _, rule := range c.PCCRules {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Precedence == rules[j].Precedence {
			return rules[i].PccRuleId < rules[j].PccRuleId
		}
		return rules[i].Precedence < rules[j].Precedence
	})
	var bindings []PathQoSBinding
	var routedPriorities []uint32
	fallbackCovered := false
	for _, rule := range rules {
		if tc := c.TrafficControlDatas[rule.RefTcDataID()]; tc != nil && len(tc.RouteToLocs) != 0 {
			// Explicit PCF routing (including TI) remains authoritative for
			// its classifier. Static fallback must not intercept it.
			routedPriorities = append(routedPriorities, uint32(rule.Precedence))
			continue
		}
		if rule.AppId != "" || len(rule.FlowInfos) == 0 {
			return nil, fmt.Errorf("cannot classify static route against PCC rule %s", rule.PccRuleId)
		}
		binding := PathQoSBinding{QFI: rule.QFI, source: rule}
		for _, info := range rule.FlowInfos {
			match, ok, matchErr := intersectULCLFlow(info, filter, []string{c.PDUAddress.String()})
			if matchErr != nil {
				return nil, matchErr
			}
			if !ok {
				continue
			}
			binding.FlowInfos = append(binding.FlowInfos, match)
			// A bidirectional unconstrained IP rule covers the fallback as well.
			original, decodeErr := strictULCLFilter(info.FlowDescription)
			if decodeErr != nil {
				return nil, decodeErr
			}
			if original.Src == "any" && (original.Dst == ulclAssignedAddress || original.Dst == route.Dst) &&
				original.Proto == flowdesc.ProtocolNumberAny && len(original.SrcPorts) == 0 &&
				len(original.DstPorts) == 0 && info.TosTrafficClass == "" && info.Spi == "" &&
				info.FlowLabel == "" && (info.FlowDirection == "" || info.FlowDirection == filter.FlowDirection) {
				fallbackCovered = true
			}
		}
		if len(binding.FlowInfos) != 0 {
			if qosID := rule.RefQosDataID(); qosID != "" {
				binding.QosData = c.QosDatas[qosID]
				if binding.QosData == nil {
					return nil, fmt.Errorf("PCC rule %s references missing QoS data %s", rule.PccRuleId, qosID)
				}
			} else {
				binding.QFI = session.DefQosQFI
			}
			bindings = append(bindings, binding)
		}
	}
	if !fallbackCovered {
		bindings = append(bindings, PathQoSBinding{
			QFI:       session.DefQosQFI,
			FlowInfos: []models.Pcf_SMPolCtrl_FlowInformation{filter},
		})
	}
	// Insert static intersections ahead of their source classifiers, while
	// retaining the order of explicit PCF routes. Never renumber PCC policy.
	used := make(map[uint32]bool)
	staticPDRs := make(map[*PDR]bool)
	for _, path := range c.Tunnel.DataPathPool {
		if path.Destination.DestinationIP == "" {
			continue
		}
		for node := path.FirstDPNode; node != nil; node = node.Next() {
			for _, tunnel := range []*GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
				for _, pdr := range tunnel.PDRs() {
					staticPDRs[pdr] = true
				}
			}
		}
	}
	for node := p.FirstDPNode; node != nil; node = node.Next() {
		if pfcp := c.PFCPContext[node.GetNodeIP()]; pfcp != nil {
			for _, pdr := range pfcp.PDRs {
				if !staticPDRs[pdr] && !pdr.Disabled && pdr.State != RULE_REMOVE {
					used[pdr.Precedence] = true
				}
			}
		}
	}
	previous := uint32(0)
	for i := range bindings {
		upper := ^uint32(0)
		if bindings[i].source != nil {
			upper = uint32(bindings[i].source.Precedence)
		}
		for _, priority := range routedPriorities {
			if priority < upper && priority > previous {
				previous = priority
			}
		}
		candidate := previous + 1
		for candidate < upper && used[candidate] {
			candidate++
		}
		if candidate >= upper || candidate == 0 {
			return nil, fmt.Errorf("no PDR precedence space for static route classifier before %d", upper)
		}
		bindings[i].Precedence, previous = candidate, candidate
		used[candidate] = true
		if err = validateQosFlowBinding(c, bindings[i].QFI, bindings[i].QosData); err != nil {
			return nil, err
		}
		if _, _, err = directionalSDFFilters(bindings[i].FlowInfos); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

func (c *SMContext) ulclChargingURRs(path *DataPath, source *PCCRule, upf *UPF) ([]*URR, error) {
	if source == nil {
		return nil, nil
	}
	level, err := source.IdentifyChargingLevel()
	if err != nil {
		return nil, err
	}
	var result []*URR
	add := func(data *models.Pcf_SMPolCtrl_ChargingData, chargingLevel ChargingLevel) error {
		if data == nil || (!data.Online && !data.Offline) {
			return nil
		}
		method := models.Chf_ConvCharging_QuotaManagementIndicator_OFFLINE_CHARGING
		if data.Online {
			method = models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING
		}
		for id, info := range c.ChargingInfo {
			if info.UpfId == upf.UUID() && info.RatingGroup == data.RatingGroup &&
				info.ChargingLevel == chargingLevel && info.ChargingMethod == method {
				if urr := c.GetUrrRule(upf.UUID(), id); urr != nil {
					result = append(result, urr)
					return nil
				}
			}
		}
		urr, info := path.CreateUrrAndChgInfo(c, data, chargingLevel, upf)
		if urr == nil || info == nil {
			return fmt.Errorf("cannot create static route charging URR for rating group %d", data.RatingGroup)
		}
		c.ChargingInfo[urr.URRID] = info
		result = append(result, urr)
		return nil
	}
	if err = add(c.ChargingData[source.RefChgDataID()], level); err != nil {
		return nil, err
	}
	if level != PduSessionCharging {
		for _, rule := range c.PCCRules {
			if chargingLevel, levelErr := rule.IdentifyChargingLevel(); levelErr == nil && chargingLevel == PduSessionCharging {
				if err = add(c.ChargingData[rule.RefChgDataID()], PduSessionCharging); err != nil {
					return nil, err
				}
			}
		}
	}
	return result, nil
}
