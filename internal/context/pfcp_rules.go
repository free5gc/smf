package context

import (
	"time"

	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/logger"
)

const (
	RULE_INITIAL RuleState = 0
	RULE_CREATE  RuleState = 1
	RULE_UPDATE  RuleState = 2
	RULE_REMOVE  RuleState = 3
	RULE_QUERY   RuleState = 4
)

type RuleState uint8

// Packet Detection Rule. Table 7.5.2.2-1
type PDR struct {
	PDRID uint16

	Precedence         uint32
	PDI                PDI
	OuterHeaderRemoval *pfcpType.OuterHeaderRemoval

	FAR *FAR
	URR []*URR
	QER []*QER

	State RuleState
}

const (
	MeasureInfoMNOP     = 0x10 // Measure Num of Pkts (MNOP)
	MeasureInfoMBQE     = 0x1  // Measure Before Qos Enforce(MQBE)
	MesureMethodVol     = "vol"
	MesureMethodTime    = "time"
	MeasurePeriodReport = 0x0100 // 0x10: PERIO
)

// Usage Report Rule
type URR struct {
	URRID                  uint32
	MeasureMethod          string // vol or time
	ReportingTrigger       pfcpType.ReportingTriggers
	MeasurementPeriod      time.Duration
	QuotaValidityTime      time.Time
	MeasurementInformation pfcpType.MeasurementInformation
	VolumeThreshold        uint64
	VolumeQuota            uint64
}

type UrrEntry struct {
	Rule     *URR      // 規則內容 (RG, Threshold...)
	State    RuleState // 部署狀態 (INITIAL, CREATE...)
	RefCount int       // number of PDRs referencing this URR
	PDRIDs   []uint16  // IDs of PDRs currently referencing this URR
}

type UrrOpt func(urr *URR)

func NewMeasureInformation(isMeasurePkt, isMeasureBeforeQos bool) UrrOpt {
	return func(urr *URR) {
		urr.MeasurementInformation.Mnop = isMeasurePkt
		urr.MeasurementInformation.Mbqe = isMeasureBeforeQos
	}
}

func NewMeasurementPeriod(time time.Duration) UrrOpt {
	return func(urr *URR) {
		urr.ReportingTrigger.Perio = true
		urr.MeasurementPeriod = time
	}
}

func NewVolumeThreshold(threshold uint64) UrrOpt {
	return func(urr *URR) {
		urr.ReportingTrigger.Volth = true
		urr.VolumeThreshold = threshold
	}
}

func NewVolumeQuota(quota uint64) UrrOpt {
	return func(urr *URR) {
		urr.ReportingTrigger.Volqu = true
		urr.VolumeQuota = quota
	}
}

func SetStartOfSDFTrigger() UrrOpt {
	return func(urr *URR) {
		urr.ReportingTrigger.Start = true
	}
}

func MeasureInformation(isMeasurePkt, isMeasureBeforeQos bool) pfcpType.MeasurementInformation {
	var measureInformation pfcpType.MeasurementInformation
	measureInformation.Mnop = isMeasurePkt
	measureInformation.Mbqe = isMeasureBeforeQos
	return measureInformation
}

func isUrrExist(urrs []*URR, urr *URR) bool { // check if urr is in URRs list
	for _, URR := range urrs {
		if urr.URRID == URR.URRID {
			return true
		}
	}
	return false
}

func (c *SMContext) RegisterUrr(upfId string, urr *URR) *URR {
	c.UrrTableMutex.Lock()
	defer c.UrrTableMutex.Unlock()
	if c.UrrTable[upfId] == nil {
		c.UrrTable[upfId] = make(map[uint32]*UrrEntry)
	}
	if entry, exists := c.UrrTable[upfId][urr.URRID]; exists {
		return entry.Rule // already registered, return existing rule
	}

	c.UrrTable[upfId][urr.URRID] = &UrrEntry{
		Rule:  urr,
		State: RULE_INITIAL,
	}
	return urr
}

func (c *SMContext) SetUrrState(upfId string, urrID uint32, state RuleState) {
	c.UrrTableMutex.Lock()
	defer c.UrrTableMutex.Unlock()

	if c.UrrTable[upfId] == nil {
		logger.PduSessLog.Warnf("SetUrrState: UPF[%s] not in UrrTable", upfId)
		return
	}
	if entry, ok := c.UrrTable[upfId][urrID]; ok {
		entry.State = state
	} else {
		logger.PduSessLog.Warnf("SetUrrState: URR[%d] not found for UPF[%s]", urrID, upfId)
	}
}

func (c *SMContext) GetUrrRule(upfId string, urrID uint32) *URR {
	c.UrrTableMutex.RLock()
	defer c.UrrTableMutex.RUnlock()
	if entries, ok := c.UrrTable[upfId]; ok {
		if entry, ok2 := entries[urrID]; ok2 {
			return entry.Rule
		}
	}
	return nil
}

func (c *SMContext) GetUrrState(upfId string, urrID uint32) RuleState {
	c.UrrTableMutex.RLock()
	defer c.UrrTableMutex.RUnlock()
	if entries, ok := c.UrrTable[upfId]; ok {
		if entry, ok2 := entries[urrID]; ok2 {
			return entry.State
		}
	}
	return RULE_INITIAL // default state if not found
}

// PDRAppendURRs appends URRs to a PDR and increments the ref count in UrrTable.
// Must be used instead of pdr.AppendURRs() whenever smContext is available.
func (c *SMContext) PDRAppendURRs(upfId string, pdr *PDR, urrs []*URR) {
	c.UrrTableMutex.Lock()
	defer c.UrrTableMutex.Unlock()
	for _, urr := range urrs {
		pdr.URR = append(pdr.URR, urr)
		entry := c.UrrTable[upfId][urr.URRID]
		if entry == nil {
			logger.PduSessLog.Warnf("[PDRAppendURRs] BUG: UPF=%s URR[%d] not in UrrTable (RegisterUrr missing?)", upfId, urr.URRID)
			continue
		}
		entry.RefCount++
		entry.PDRIDs = append(entry.PDRIDs, pdr.PDRID)
		logger.PduSessLog.Tracef("[PDRAppendURRs] UPF=%s URR[%d] refCount=%d PDRs=%v",
			upfId, urr.URRID, entry.RefCount, entry.PDRIDs)
	}
}

// PDRReleaseURRs decrements the ref count for all URRs on a PDR.
// URRs whose ref count reaches 0 are marked RULE_REMOVE so that
// ActivateUPFSession will include them in the PFCP RemoveURR list.
func (c *SMContext) PDRReleaseURRs(upfId string, pdr *PDR) {
	if pdr == nil {
		return
	}
	c.UrrTableMutex.Lock()
	defer c.UrrTableMutex.Unlock()
	for _, urr := range pdr.URR {
		entry := c.UrrTable[upfId][urr.URRID]
		if entry == nil {
			logger.PduSessLog.Warnf("[PDRReleaseURRs] BUG: UPF=%s URR[%d] not in UrrTable", upfId, urr.URRID)
			continue
		}
		entry.RefCount--
		// remove this PDR from the tracking list
		for i, id := range entry.PDRIDs {
			if id == pdr.PDRID {
				entry.PDRIDs = append(entry.PDRIDs[:i], entry.PDRIDs[i+1:]...)
				break
			}
		}
		if entry.RefCount <= 0 {
			entry.State = RULE_REMOVE
			logger.PduSessLog.Tracef("[PDRReleaseURRs] UPF=%s URR[%d] refCount=0 → RULE_REMOVE", upfId, urr.URRID)
		} else {
			logger.PduSessLog.Tracef("[PDRReleaseURRs] UPF=%s URR[%d] refCount=%d PDRs=%v",
				upfId, urr.URRID, entry.RefCount, entry.PDRIDs)
		}
	}
}

// DeleteFromUrrTable removes a URR entry from UrrTable after the PFCP RemoveURR
// has been acknowledged and the UPF pool entry freed.
func (c *SMContext) DeleteFromUrrTable(upfId string, urrID uint32) {
	c.UrrTableMutex.Lock()
	defer c.UrrTableMutex.Unlock()
	if entries, ok := c.UrrTable[upfId]; ok {
		delete(entries, urrID)
	}
}

// Packet Detection. 7.5.2.2-2
type PDI struct {
	SourceInterface pfcpType.SourceInterface
	LocalFTeid      *pfcpType.FTEID
	NetworkInstance *pfcpType.NetworkInstance
	UEIPAddress     *pfcpType.UEIPAddress
	SDFFilter       *pfcpType.SDFFilter
	ApplicationID   string
}

// Forwarding Action Rule. 7.5.2.3-1
type FAR struct {
	FARID uint32

	ApplyAction          pfcpType.ApplyAction
	ForwardingParameters *ForwardingParameters

	BAR   *BAR
	State RuleState
}

// Forwarding Parameters. 7.5.2.3-2
type ForwardingParameters struct {
	DestinationInterface pfcpType.DestinationInterface
	NetworkInstance      *pfcpType.NetworkInstance
	OuterHeaderCreation  *pfcpType.OuterHeaderCreation
	ForwardingPolicyID   string
	SendEndMarker        bool
}

// Buffering Action Rule 7.5.2.6-1
type BAR struct {
	BARID uint8

	DownlinkDataNotificationDelay  pfcpType.DownlinkDataNotificationDelay
	SuggestedBufferingPacketsCount pfcpType.SuggestedBufferingPacketsCount

	State RuleState
}

// QoS Enhancement Rule
type QER struct {
	QERID uint32

	QFI pfcpType.QFI

	GateStatus *pfcpType.GateStatus
	MBR        *pfcpType.MBR
	GBR        *pfcpType.GBR

	State RuleState
}
