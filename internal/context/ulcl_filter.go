package context

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/util/flowdesc"
)

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
