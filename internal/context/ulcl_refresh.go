package context

// Rebuild static classifiers after PCC updates. Keep retired PDRs until the N4
// response; new classifiers reuse tunnel endpoints and the existing shared FARs.
func (c *SMContext) refreshULCLQoSBindings() error {
	plans := make(map[*DataPath][]PathQoSBinding)
	for _, path := range c.Tunnel.DataPathPool {
		if !path.Activated || path.Destination.DestinationIP == "" || c.DataPathToBeRemoved[path.PathID] != nil {
			continue
		}
		bindings, err := path.ULCLQoSBindings(c)
		if err != nil {
			return err
		}
		plans[path] = bindings
	}
	for path, bindings := range plans {
		for node := path.FirstDPNode; node != nil; node = node.Next() {
			for _, tunnel := range []*GTPTunnel{node.UpLinkTunnel, node.DownLinkTunnel} {
				old := append([]*PDR{tunnel.PDR}, tunnel.AdditionalPDRs...)
				template := *tunnel.PDR
				template.URR = nil
				for _, urr := range tunnel.PDR.URR {
					if c.ChargingInfo[urr.URRID] == nil {
						template.URR = append(template.URR, urr)
					}
				}
				pdr, err := node.UPF.AddPDR()
				if err != nil {
					return err
				}
				if err = node.UPF.RemoveFAR(pdr.FAR); err != nil {
					return err
				}
				id := pdr.PDRID
				*pdr = template
				pdr.PDRID, pdr.State, pdr.QER, pdr.URR = id, RULE_INITIAL, nil, nil
				c.PDRAppendURRs(node.UPF.UUID(), pdr, template.URR)
				if err = c.PutPDRtoPFCPSession(node.UPF.NodeID, pdr); err != nil {
					return err
				}
				for _, retired := range old {
					c.PDRReleaseURRs(node.UPF.UUID(), retired)
					retired.State = RULE_REMOVE
					// Disabled primary PDRs were never installed, and must not
					// generate a Remove PDR for an unknown UPF rule.
				}
				tunnel.RetiringPDRs = append(tunnel.RetiringPDRs, old...)
				tunnel.PDR, tunnel.AdditionalPDRs = pdr, nil
			}
		}
		if err := path.applyQoSBindings(c, 255, bindings); err != nil {
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
