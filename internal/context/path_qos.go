package context

import (
	"encoding/hex"
	"fmt"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/util/flowdesc"
)

// A plan contains normalized desired rules, independent of PDR/QER/URR IDs and
// mutable PCC objects. Compile once per path, then reconcile on each UPF.
type pathQoSPlan [2][]pathQoSRule

type pathQoSRule struct {
	filter     *pfcptype.SDFFilter
	precedence uint32
	profile    *QER
	ambr       bool
	charging   map[chargingRule]struct{}
}

func compilePathQoS(c *SMContext, precedence uint32, bindings []PathQoSBinding) (pathQoSPlan, error) {
	var plan pathQoSPlan
	var sessionCharging map[chargingRule]struct{}
	chargingLoaded := false
	for _, binding := range bindings {
		profile, err := flowQERProfile(c, binding.QFI, binding.QosData)
		if err != nil {
			return plan, err
		}
		if binding.source != nil && !chargingLoaded {
			sessionCharging = c.ulclSessionChargingRules()
			chargingLoaded = true
		}
		charging, err := c.ulclChargingRules(binding.source, sessionCharging)
		if err != nil {
			return plan, err
		}
		filters := [2][]*pfcptype.SDFFilter{{nil}, {nil}}
		if len(binding.FlowInfos) != 0 {
			filters[0], filters[1], err = directionalSDFFilters(binding.FlowInfos)
			if err != nil {
				return plan, err
			}
		}
		priority := precedence
		if binding.Precedence != 0 {
			priority = binding.Precedence
		}
		for direction, list := range filters {
			for _, filter := range list {
				plan[direction] = append(plan[direction], pathQoSRule{
					filter: filter, precedence: priority, profile: profile,
					ambr: binding.QFI == c.SelectedSessionRule().DefQosQFI || !isGBRFlow(binding.QosData), charging: charging,
				})
			}
		}
	}
	return plan, nil
}

func (p *DataPath) applyQoSBindings(c *SMContext, precedence uint32, bindings []PathQoSBinding) error {
	plan, err := compilePathQoS(c, precedence, bindings)
	if err != nil {
		return err
	}
	return p.reconcileQoSPlan(c, plan)
}

func (p *DataPath) reconcileQoSPlan(c *SMContext, plan pathQoSPlan) error {
	for node := p.FirstDPNode; node != nil; node = node.Next() {
		for direction, tunnel := range []*GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			if err := p.reconcileTunnelQoS(c, node, tunnel, plan[direction]); err != nil {
				return err
			}
		}
	}
	return nil
}

// PDRs includes all classifiers sharing a tunnel's TEID and forwarding rule.
func (t *GTPTunnel) PDRs() []*PDR {
	if t == nil || t.PDR == nil {
		return nil
	}
	result := append([]*PDR{t.PDR}, t.AdditionalPDRs...)
	return append(result, t.RetiringPDRs...)
}

// Comparable classifier values allow linear matching without JSON, reflection,
// a persisted fingerprint, or dependence on filter ordering in a notification.
type pathClassifierKey struct {
	precedence uint32
	qfi        uint8
	present    bool
	flags      [5]bool
	flow, tos  string
	spi, label string
	filterID   uint32
}

func classifierKey(precedence uint32, qfi uint8, filter *pfcptype.SDFFilter) pathClassifierKey {
	key := pathClassifierKey{precedence: precedence, qfi: qfi}
	if filter != nil {
		key.present = true
		key.flags = [5]bool{filter.Bid, filter.Fl, filter.Spi, filter.Ttc, filter.Fd}
		key.flow, key.tos = string(filter.FlowDescription), string(filter.TosTrafficClass)
		key.spi, key.label, key.filterID = string(filter.SecurityParameterIndex), string(filter.FlowLabel), filter.SdfFilterId
	}
	return key
}

func (rule pathQoSRule) matches(c *SMContext, node *DataPathNode, pdr *PDR) bool {
	flow, ambr := false, false
	for _, qer := range pdr.QER {
		if qer.QFI != nil {
			if flow || !sameQERProfile(qer, rule.profile) {
				return false
			}
			flow = true
		} else {
			if ambr || qer.QERID != c.AMBRQerMap[node.UPF.GetUUID()] {
				return false
			}
			ambr = true
		}
	}
	if !flow || ambr != rule.ambr {
		return false
	}
	expected := rule.charging
	if !node.IsAnchorUPF() {
		expected = nil
	}
	seen := make(map[chargingRule]struct{}, len(expected))
	for _, urr := range pdr.URR {
		if info := c.ChargingInfo[urr.URRID]; info != nil {
			identity := chargingIdentity(info)
			if _, ok := expected[identity]; !ok || info.UpfId != node.UPF.UUID() {
				return false
			}
			seen[identity] = struct{}{}
		}
	}
	return len(seen) == len(expected)
}

func (p *DataPath) reconcileTunnelQoS(c *SMContext, node *DataPathNode, tunnel *GTPTunnel, rules []pathQoSRule) error {
	old := append([]*PDR{tunnel.PDR}, tunnel.AdditionalPDRs...)
	index := make(map[pathClassifierKey][]*PDR, len(old))
	for _, pdr := range old {
		if !pdr.Disabled && pdr.State != RULE_REMOVE {
			for _, qer := range pdr.QER {
				if qer.QFI != nil {
					key := classifierKey(pdr.Precedence, qer.QFI.QFI, pdr.PDI.SDFFilter)
					index[key] = append(index[key], pdr)
					break
				}
			}
		}
	}
	var measurement []*URR
	for _, urr := range tunnel.PDR.URR {
		if c.ChargingInfo[urr.URRID] == nil {
			measurement = append(measurement, urr)
		}
	}
	kept := make(map[*PDR]bool, len(old))
	current := make([]*PDR, 0, len(rules))
	for _, rule := range rules {
		key := classifierKey(rule.precedence, rule.profile.QFI.QFI, rule.filter)
		if candidates := index[key]; len(candidates) != 0 {
			candidate := candidates[0]
			index[key] = candidates[1:]
			if rule.matches(c, node, candidate) {
				kept[candidate] = true
				current = append(current, candidate)
				continue
			}
		}
		qer, err := flowQER(c, node.UPF, rule.profile)
		if err != nil {
			return err
		}
		var charging []*URR
		if node.IsAnchorUPF() {
			charging, err = c.ulclChargingURRs(p, rule.charging, node.UPF)
			if err != nil {
				return err
			}
		}
		pdr, err := p.qosPDR(c, node, tunnel.PDR, !p.Activated && len(current) == 0)
		if err != nil {
			return err
		}
		pdr.PDI.SDFFilter, pdr.Precedence = rule.filter, rule.precedence
		if rule.ambr {
			ambr := node.UPF.GetQERById(c.AMBRQerMap[node.UPF.GetUUID()])
			if ambr == nil {
				return fmt.Errorf("missing Session-AMBR QER for UPF %s", node.UPF.UUID())
			}
			appendQER(pdr, ambr)
		}
		appendQER(pdr, qer)
		c.PDRAppendURRs(node.UPF.UUID(), pdr, measurement)
		c.PDRAppendURRs(node.UPF.UUID(), pdr, charging)
		kept[pdr] = true
		current = append(current, pdr)
	}
	if len(current) == 0 {
		pdr := tunnel.PDR
		if !pdr.Disabled {
			var err error
			pdr, err = p.qosPDR(c, node, pdr, !p.Activated)
			if err != nil {
				return err
			}
			pdr.Disabled = true
		}
		kept[pdr] = true
		current = append(current, pdr)
	}
	for _, pdr := range old {
		if !kept[pdr] {
			c.PDRReleaseURRs(node.UPF.UUID(), pdr)
			pdr.State = RULE_REMOVE
			tunnel.RetiringPDRs = append(tunnel.RetiringPDRs, pdr)
		}
	}
	tunnel.PDR, tunnel.AdditionalPDRs = current[0], current[1:]
	return nil
}

// Replacement classifiers keep the tunnel endpoint and shared FAR. Reuse the
// initial template only before activation; installed PDRs remain until retirement.
func (p *DataPath) qosPDR(c *SMContext, node *DataPathNode, template *PDR, initial bool) (*PDR, error) {
	pdr := template
	if initial {
		c.PDRReleaseURRs(node.UPF.UUID(), pdr)
	} else {
		var err error
		pdr, err = node.UPF.AddPDR()
		if err != nil {
			return nil, err
		}
		if err = node.UPF.RemoveFAR(pdr.FAR); err != nil {
			return nil, err
		}
	}
	id := pdr.PDRID
	*pdr = *template
	pdr.PDRID, pdr.State, pdr.Disabled = id, RULE_INITIAL, false
	pdr.QER, pdr.URR = nil, nil
	if err := c.PutPDRtoPFCPSession(node.UPF.NodeID, pdr); err != nil {
		return nil, err
	}
	return pdr, nil
}

// Keep one SDF per PDR: deployed UPFs may only retain one SDF IE per PDI.
// Each direction's filters share the original tunnel's FAR, QERs and URRs.
func directionalSDFFilters(infos []models.Pcf_SMPolCtrl_FlowInformation) (
	ul, dl []*pfcptype.SDFFilter, err error,
) {
	for _, info := range infos {
		if _, err = flowdesc.Decode(info.FlowDescription); err != nil {
			return nil, nil, err
		}
		sdf := &pfcptype.SDFFilter{
			Fd: true, FlowDescription: []byte(info.FlowDescription),
			LengthOfFlowDescription: uint16(len(info.FlowDescription)),
		}
		fields := []struct {
			text string
			size int
			dst  *[]byte
			flag *bool
		}{
			{info.TosTrafficClass, 2, &sdf.TosTrafficClass, &sdf.Ttc},
			{info.Spi, 4, &sdf.SecurityParameterIndex, &sdf.Spi},
			{info.FlowLabel, 3, &sdf.FlowLabel, &sdf.Fl},
		}
		for _, field := range fields {
			if field.text == "" {
				continue
			}
			value, decodeErr := hex.DecodeString(field.text)
			if decodeErr != nil || len(value) != field.size {
				return nil, nil, fmt.Errorf("invalid SDF hexadecimal field %q", field.text)
			}
			*field.dst, *field.flag = value, true
		}
		switch info.FlowDirection {
		case models.Pcf_SMPolCtrl_FlowDirectionRm_UPLINK:
			ul = append(ul, sdf)
		case models.Pcf_SMPolCtrl_FlowDirectionRm_DOWNLINK:
			dl = append(dl, sdf)
		case "", models.Pcf_SMPolCtrl_FlowDirectionRm_BIDIRECTIONAL:
			ul, dl = append(ul, sdf), append(dl, sdf)
		default:
			return nil, nil, fmt.Errorf("unsupported flow direction %q", info.FlowDirection)
		}
	}
	return ul, dl, nil
}

func (p *DataPath) applyFlowInfos(c *SMContext, infos []models.Pcf_SMPolCtrl_FlowInformation) error {
	ul, dl, err := directionalSDFFilters(infos)
	if err != nil {
		return err
	}
	for node := p.FirstDPNode; node != nil; node = node.Next() {
		for i, tunnel := range []*GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			filters := ul
			if i == 1 {
				filters = dl
			}
			tunnel.PDR.Disabled = len(filters) == 0
			if len(filters) == 0 {
				continue
			}
			tunnel.PDR.PDI.SDFFilter = filters[0]
			for _, filter := range filters[1:] {
				pdr, allocErr := node.UPF.AddPDR()
				if allocErr != nil {
					return allocErr
				}
				// AddPDR also allocates a FAR; use the original shared FAR instead.
				if err = node.UPF.RemoveFAR(pdr.FAR); err != nil {
					return err
				}
				id := pdr.PDRID
				*pdr = *tunnel.PDR
				pdr.PDRID, pdr.State, pdr.PDI.SDFFilter = id, RULE_INITIAL, filter
				pdr.URR = nil
				c.PDRAppendURRs(node.UPF.UUID(), pdr, tunnel.PDR.URR)
				tunnel.AdditionalPDRs = append(tunnel.AdditionalPDRs, pdr)
				if err = c.PutPDRtoPFCPSession(node.UPF.NodeID, pdr); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (tunnel *GTPTunnel) releaseAdditionalPDRs(c *SMContext, upf *UPF) {
	tunnel.releaseFilterPDRs(c, upf, tunnel.AdditionalPDRs)
	tunnel.AdditionalPDRs = nil
	tunnel.releaseRetiredPDRs(c, upf)
}

func (tunnel *GTPTunnel) releaseRetiredPDRs(c *SMContext, upf *UPF) {
	tunnel.releaseFilterPDRs(c, upf, tunnel.RetiringPDRs)
	tunnel.RetiringPDRs = nil
}

func (tunnel *GTPTunnel) releaseFilterPDRs(c *SMContext, upf *UPF, pdrs []*PDR) {
	for _, pdr := range pdrs {
		c.RemovePDRfromPFCPSession(upf.NodeID, pdr)
		if err := upf.RemovePDR(pdr); err != nil {
			c.Log.Warnf("Remove filter PDR: %v", err)
		}
		for _, qer := range pdr.QER {
			if err := c.releaseQER(upf, qer); err != nil {
				c.Log.Warnf("Remove filter QER: %v", err)
			}
		}
		for _, urr := range pdr.URR {
			if c.GetUrrState(upf.UUID(), urr.URRID) == RULE_REMOVE {
				if err := upf.RemoveURR(urr); err != nil {
					c.Log.Warnf("Remove filter URR: %v", err)
				}
				c.DeleteFromUrrTable(upf.UUID(), urr.URRID)
			}
		}
	}
}
