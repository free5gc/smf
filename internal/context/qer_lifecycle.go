package context

import "strings"

// appendQER avoids duplicate references when activation already attached the
// default-flow QER before applying an explicit default-flow PCC rule.
func appendQER(pdr *PDR, qer *QER) {
	for _, current := range pdr.QER {
		if current != nil && current.QERID == qer.QERID {
			return
		}
	}
	pdr.QER = append(pdr.QER, qer)
}

// A QER may be shared by both directions and several PCC datapaths. Its ID
// remains allocated until the last PDR leaves the UPF's local pool.
func (c *SMContext) releaseQER(upf *UPF, qer *QER) error {
	inUse := false
	upf.pdrPool.Range(func(_, value interface{}) bool {
		for _, current := range value.(*PDR).QER {
			if current != nil && current.QERID == qer.QERID {
				inUse = true
				return false
			}
		}
		return true
	})
	if inUse || upf.GetQERById(qer.QERID) == nil {
		return nil
	}
	if err := upf.RemoveQER(qer); err != nil {
		return err
	}
	for key, id := range c.QerUpfMap {
		if id == qer.QERID && strings.HasPrefix(key, upf.UUID()+":") {
			delete(c.QerUpfMap, key)
		}
	}
	if c.AMBRQerMap[upf.GetUUID()] == qer.QERID {
		delete(c.AMBRQerMap, upf.GetUUID())
	}
	return nil
}
