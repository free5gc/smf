package context

import (
	"net"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/context/pool"
)

type SNssai struct {
	Sst int32
	Sd  string
}

// Equal return true if two S-NSSAI is equal
func (s *SNssai) Equal(target *SNssai) bool {
	return s.Sst == target.Sst && s.Sd == target.Sd
}

type SnssaiUPFInfo struct {
	SNssai  SNssai
	DnnList []DnnUPFInfoItem
}

// DnnUpfInfoItem presents UPF dnn information
type DnnUPFInfoItem struct {
	Dnn             string
	DnaiList        []string
	PduSessionTypes []models.PduSessionType
	UeIPPools       []*UeIPPool
}

// UeIPPool represent IP address pool for UE
type UeIPPool struct {
	ueSubNet *net.IPNet
	pool     *pool.LazyReusePool
}

// ContainsDNAI return true if the this dnn Info contains the specify DNAI
func (d *DnnUPFInfoItem) ContainsDNAI(targetDnai string) bool {
	if targetDnai == "" {
		return d.DnaiList == nil || len(d.DnaiList) == 0
	}
	for _, dnai := range d.DnaiList {
		if dnai == targetDnai {
			return true
		}
	}
	return false
}
