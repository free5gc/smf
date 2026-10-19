package context

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
