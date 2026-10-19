package processor

import "github.com/free5gc/smf/internal/context"

// Include rules from every classifier, including downlink-only QoS/charging.
func pfcpStateForTunnels(upf *context.UPF, tunnels ...*context.GTPTunnel) *PFCPState {
	state := &PFCPState{upf: upf}
	pdrs, fars := make(map[uint16]bool), make(map[uint32]bool)
	qers, urrs := make(map[uint32]bool), make(map[uint32]bool)
	for _, tunnel := range tunnels {
		for _, pdr := range tunnel.PDRs() {
			if pdrs[pdr.PDRID] {
				continue
			}
			pdrs[pdr.PDRID] = true
			state.pdrList = append(state.pdrList, pdr)
			if pdr.FAR != nil && !fars[pdr.FAR.FARID] {
				fars[pdr.FAR.FARID] = true
				state.farList = append(state.farList, pdr.FAR)
			}
			for _, qer := range pdr.QER {
				if !qers[qer.QERID] {
					qers[qer.QERID] = true
					state.qerList = append(state.qerList, qer)
				}
			}
			for _, urr := range pdr.URR {
				if !urrs[urr.URRID] {
					urrs[urr.URRID] = true
					state.urrList = append(state.urrList, urr)
				}
			}
		}
	}
	return state
}
