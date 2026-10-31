package context

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

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

// chargingRule contains only the authorization that changes a charging URR.
// Runtime quota, timers and URR IDs are deliberately excluded.
type chargingRule struct {
	ratingGroup int32
	level       ChargingLevel
	online      bool
}

func (c *SMContext) ulclSessionChargingRules() map[chargingRule]struct{} {
	result := make(map[chargingRule]struct{})
	for _, rule := range c.PCCRules {
		data := c.ChargingData[rule.RefChgDataID()]
		if data == nil || (!data.Online && !data.Offline) {
			continue
		}
		if level, err := rule.IdentifyChargingLevel(); err == nil && level == PduSessionCharging {
			result[chargingRule{data.RatingGroup, level, data.Online}] = struct{}{}
		}
	}
	return result
}

func (c *SMContext) ulclChargingRules(
	source *PCCRule, session map[chargingRule]struct{},
) (map[chargingRule]struct{}, error) {
	if source == nil {
		return nil, nil
	}
	data := c.ChargingData[source.RefChgDataID()]
	if (data == nil || (!data.Online && !data.Offline)) && len(session) == 0 {
		return nil, nil
	}
	level, err := source.IdentifyChargingLevel()
	if err != nil {
		return nil, err
	}
	result := make(map[chargingRule]struct{}, len(session)+1)
	if data != nil && (data.Online || data.Offline) {
		result[chargingRule{data.RatingGroup, level, data.Online}] = struct{}{}
	}
	if level != PduSessionCharging {
		for rule := range session {
			result[rule] = struct{}{}
		}
	}
	return result, nil
}

func chargingIdentity(info *ChargingInfo) chargingRule {
	return chargingRule{
		info.RatingGroup, info.ChargingLevel,
		info.ChargingMethod == models.Chf_ConvCharging_QuotaManagementIndicator_ONLINE_CHARGING,
	}
}

func (c *SMContext) ulclChargingURRs(path *DataPath, rules map[chargingRule]struct{}, upf *UPF) ([]*URR, error) {
	var result []*URR
	for rule := range rules {
		var existing *URR
		for id, info := range c.ChargingInfo {
			if info.UpfId == upf.UUID() && chargingIdentity(info) == rule {
				if urr := c.GetUrrRule(upf.UUID(), id); urr != nil {
					existing = urr
					break
				}
			}
		}
		if existing != nil {
			result = append(result, existing)
			continue
		}
		data := &models.Pcf_SMPolCtrl_ChargingData{
			RatingGroup: rule.ratingGroup, Online: rule.online, Offline: !rule.online,
		}
		urr, info := path.CreateUrrAndChgInfo(c, data, rule.level, upf)
		if urr == nil || info == nil {
			return nil, fmt.Errorf("cannot create static route charging URR for rating group %d", rule.ratingGroup)
		}
		c.ChargingInfo[urr.URRID] = info
		result = append(result, urr)
	}
	return result, nil
}

// Intersections retain both filters' address, protocol, port and direction restrictions.
// Reject syntax Decode would silently discard rather than widening a static route.
func strictULCLFilter(description string) (*flowdesc.IPFilterRule, error) {
	rule, err := flowdesc.Decode(description)
	if err != nil {
		return nil, err
	}
	encoded, err := flowdesc.Encode(rule)
	if err != nil {
		return nil, err
	}
	input := strings.Fields(strings.ToLower(description))
	output := strings.Fields(encoded)
	if len(input) != len(output) || rule.Action != flowdesc.Permit {
		return nil, fmt.Errorf("unsupported static route filter: %s", description)
	}
	if input[2] != "ip" {
		if _, parseErr := strconv.ParseUint(input[2], 10, 8); parseErr != nil {
			return nil, fmt.Errorf("invalid static route IP protocol: %s", input[2])
		}
	}
	for _, ports := range []flowdesc.PortRanges{rule.SrcPorts, rule.DstPorts} {
		for _, port := range ports {
			if port.Start > port.End {
				return nil, fmt.Errorf("invalid TI port range: %s", port.String())
			}
		}
	}
	if rule.Proto == flowdesc.ProtocolNumberAny && input[2] != "ip" {
		return nil, fmt.Errorf("unsupported static route filter: %s", description)
	}
	return rule, nil
}

func intersectULCLAddress(a, b string, assigned []string) (string, bool, error) {
	if a == b || b == "any" {
		return a, true, nil
	}
	if a == "any" {
		return b, true, nil
	}
	parseAddress := func(value string) (netip.Prefix, error) {
		if address, err := netip.ParseAddr(value); err == nil {
			return netip.PrefixFrom(address, address.BitLen()), nil
		}
		return netip.ParsePrefix(value)
	}
	parse := func(value string) (netip.Prefix, error) {
		if value != ulclAssignedAddress {
			return parseAddress(value)
		}
		other := a
		if a == ulclAssignedAddress {
			other = b
		}
		peer, err := parseAddress(other)
		if err != nil {
			return netip.Prefix{}, err
		}
		for _, candidate := range assigned {
			prefix, parseErr := parseAddress(candidate)
			if parseErr == nil && prefix.Addr().Is4() == peer.Addr().Is4() {
				return prefix, nil
			}
		}
		return netip.Prefix{}, fmt.Errorf("no assigned UE address for %s", other)
	}
	pa, err := parse(a)
	if err != nil {
		return "", false, fmt.Errorf("cannot resolve filter address %q: %w", a, err)
	}
	pb, err := parse(b)
	if err != nil {
		return "", false, fmt.Errorf("cannot resolve filter address %q: %w", b, err)
	}
	if !pa.Overlaps(pb) {
		return "", false, nil
	}
	if pa.Bits() >= pb.Bits() {
		return a, true, nil
	}
	return b, true, nil
}

func intersectULCLPorts(a, b flowdesc.PortRanges) (flowdesc.PortRanges, bool) {
	if len(a) == 0 {
		return b, true
	}
	if len(b) == 0 {
		return a, true
	}
	var result flowdesc.PortRanges
	for _, first := range a {
		for _, second := range b {
			start, end := max(first.Start, second.Start), min(first.End, second.End)
			if start <= end {
				result = append(result, flowdesc.PortRange{Start: start, End: end})
			}
		}
	}
	return result, len(result) > 0
}

func intersectULCLFlow(base, ti models.Pcf_SMPolCtrl_FlowInformation, assigned []string) (
	models.Pcf_SMPolCtrl_FlowInformation, bool, error,
) {
	result := base
	if base.EthFlowDescription != nil || ti.EthFlowDescription != nil {
		return result, false, fmt.Errorf("ethernet static route filter intersection is not supported")
	}
	a, err := strictULCLFilter(base.FlowDescription)
	if err != nil {
		return result, false, err
	}
	b, err := strictULCLFilter(ti.FlowDescription)
	if err != nil {
		return result, false, err
	}
	if a.Proto != flowdesc.ProtocolNumberAny && b.Proto != flowdesc.ProtocolNumberAny && a.Proto != b.Proto {
		return result, false, nil
	}
	if a.Proto == flowdesc.ProtocolNumberAny {
		a.Proto = b.Proto
	}
	var ok bool
	if a.Src, ok, err = intersectULCLAddress(a.Src, b.Src, assigned); err != nil || !ok {
		return result, false, err
	}
	if a.Dst, ok, err = intersectULCLAddress(a.Dst, b.Dst, assigned); err != nil || !ok {
		return result, false, err
	}
	if a.SrcPorts, ok = intersectULCLPorts(a.SrcPorts, b.SrcPorts); !ok {
		return result, false, nil
	}
	if a.DstPorts, ok = intersectULCLPorts(a.DstPorts, b.DstPorts); !ok {
		return result, false, nil
	}
	if base.FlowDirection == "" || base.FlowDirection == models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL {
		result.FlowDirection = ti.FlowDirection
	} else if ti.FlowDirection != "" && ti.FlowDirection != models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL &&
		ti.FlowDirection != base.FlowDirection {
		return result, false, nil
	}
	result.FlowDescription, err = flowdesc.Encode(a)
	result.PackFiltId = "" // The new rule does not own the source rule's packet filter ID.
	return result, true, err
}

// Compile all desired static rules before changing any classifier. Reconcile
// against the existing PDRs so an unrelated or repeated update preserves IDs.
func (c *SMContext) refreshULCLQoSBindings() error {
	plans := make(map[*DataPath]pathQoSPlan)
	for _, path := range c.Tunnel.DataPathPool {
		if !path.Activated || path.Destination.DestinationIP == "" || c.DataPathToBeRemoved[path.PathID] != nil {
			continue
		}
		bindings, err := path.ULCLQoSBindings(c)
		if err != nil {
			return err
		}
		plan, err := compilePathQoS(c, 255, bindings)
		if err != nil {
			return err
		}
		plans[path] = plan
	}
	for path, plan := range plans {
		if err := path.reconcileQoSPlan(c, plan); err != nil {
			return err
		}
	}
	return nil
}

func (c *SMContext) removeRetiredULCLPDRs() {
	for _, path := range c.Tunnel.DataPathPool {
		for node := path.FirstDPNode; node != nil; node = node.Next() {
			for _, tunnel := range []*GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
				if tunnel == nil || len(tunnel.RetiringPDRs) == 0 {
					continue
				}
				// The FAR remains shared by the replacement PDRs.
				tunnel.releaseRetiredPDRs(c, node.UPF)
			}
		}
	}
}
