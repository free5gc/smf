package context

import (
	"fmt"

	"github.com/free5gc/smf/internal/pfcp/pfcptype"
)

func (p *DataPath) applyQoSBindings(c *SMContext, precedence uint32, bindings []PathQoSBinding) error {
	for node := p.FirstDPNode; node != nil; node = node.Next() {
		for direction, tunnel := range []*GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
			template := *tunnel.PDR
			c.PDRReleaseURRs(node.UPF.UUID(), tunnel.PDR)
			tunnel.PDR.URR = nil
			count := 0
			for _, binding := range bindings {
				filters := []*pfcptype.SDFFilter{nil}
				if len(binding.FlowInfos) != 0 {
					ul, dl, err := directionalSDFFilters(binding.FlowInfos)
					if err != nil {
						return err
					}
					filters = ul
					if direction == 1 {
						filters = dl
					}
				}
				if len(filters) == 0 {
					continue
				}
				qer, err := flowQER(c, node.UPF, binding.QFI, binding.QosData)
				if err != nil {
					return err
				}
				var charging []*URR
				if node.IsAnchorUPF() {
					charging, err = c.ulclChargingURRs(p, binding.source, node.UPF)
					if err != nil {
						return err
					}
				}
				for _, filter := range filters {
					pdr := tunnel.PDR
					if count != 0 {
						pdr, err = node.UPF.AddPDR()
						if err != nil {
							return err
						}
						if err = node.UPF.RemoveFAR(pdr.FAR); err != nil {
							return err
						}
						tunnel.AdditionalPDRs = append(tunnel.AdditionalPDRs, pdr)
						if err = c.PutPDRtoPFCPSession(node.UPF.NodeID, pdr); err != nil {
							return err
						}
					}
					id := pdr.PDRID
					*pdr = template
					pdr.PDRID, pdr.State, pdr.Disabled = id, RULE_INITIAL, false
					pdr.URR, pdr.QER = nil, nil
					pdr.PDI.SDFFilter = filter
					pdr.Precedence = precedence
					if binding.Precedence != 0 {
						pdr.Precedence = binding.Precedence
					}
					if binding.QFI == c.SelectedSessionRule().DefQosQFI || !isGBRFlow(binding.QosData) {
						ambr := node.UPF.GetQERById(c.AMBRQerMap[node.UPF.GetUUID()])
						if ambr == nil {
							return fmt.Errorf("missing Session-AMBR QER for UPF %s", node.UPF.UUID())
						}
						appendQER(pdr, ambr)
					}
					appendQER(pdr, qer)
					c.PDRAppendURRs(node.UPF.UUID(), pdr, template.URR)
					c.PDRAppendURRs(node.UPF.UUID(), pdr, charging)
					count++
				}
			}
			if count == 0 {
				tunnel.PDR.Disabled = true
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
