package context

import (
	"encoding/hex"
	"fmt"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/util/flowdesc"
)

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
	for _, pdr := range tunnel.AdditionalPDRs {
		c.RemovePDRfromPFCPSession(upf.NodeID, pdr)
		if err := upf.RemovePDR(pdr); err != nil {
			c.Log.Warnf("Remove filter PDR: %v", err)
		}
	}
	tunnel.AdditionalPDRs = nil
}
