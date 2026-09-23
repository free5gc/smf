package context

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"

	nasie "github.com/free5gc/nas/ie"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

var upfPool sync.Map

type UPTunnel struct {
	PathIDGenerator *idgenerator.IDGenerator
	DataPathPool    DataPathPool
	ANInformation   struct {
		IPAddress net.IP
		TEID      uint32
	}
}

func (t *UPTunnel) UpdateANInformation(ip net.IP, teid uint32) {
	t.ANInformation.IPAddress = ip
	t.ANInformation.TEID = teid

	for _, dataPath := range t.DataPathPool {
		if dataPath.Activated {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR

			if DLPDR.FAR.ForwardingParameters.OuterHeaderCreation != nil {
				// Old AN tunnel exists
				DLPDR.FAR.ForwardingParameters.SendEndMarker = true
			}

			DLPDR.FAR.ForwardingParameters.OuterHeaderCreation = new(pfcptype.OuterHeaderCreation)
			dlOuterHeaderCreation := DLPDR.FAR.ForwardingParameters.OuterHeaderCreation
			dlOuterHeaderCreation.OuterHeaderCreationDescription = pfcptype.OuterHeaderCreationGtpUUdpIpv4
			dlOuterHeaderCreation.Teid = t.ANInformation.TEID
			dlOuterHeaderCreation.Ipv4Address = t.ANInformation.IPAddress.To4()
			DLPDR.FAR.State = RULE_UPDATE
		}
	}
}

type UPFAssociationState uint8

const (
	AssociationDown UPFAssociationState = iota
	AssociationSettingUp
	AssociationEstablished
	AssociationReleasing
)

var closedAssociationDone = func() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

type UPF struct {
	uuid                uuid.UUID
	NodeID              pfcptype.NodeID
	Addr                string
	recoveryTimeStampMu sync.RWMutex
	recoveryTimeStamp   time.Time

	associationMu         sync.RWMutex
	associationState      UPFAssociationState
	associationContext    context.Context
	cancelAssociation     context.CancelFunc
	associationGeneration uint64
	associationLifecycle  bool

	upFunctionFeaturesMu sync.RWMutex
	upFunctionFeatures   []byte

	// sessionWorkGate coordinates in-flight PFCP Session Establishment and
	// Modification with PFCP Association Release.
	sessionWorkGate sync.RWMutex

	SNssaiInfos  []*SnssaiUPFInfo
	N3Interfaces []*UPFInterfaceInfo
	N9Interfaces []*UPFInterfaceInfo

	pdrPool sync.Map
	farPool sync.Map
	barPool sync.Map
	qerPool sync.Map
	urrPool sync.Map

	pdrIDGenerator *idgenerator.IDGenerator
	farIDGenerator *idgenerator.IDGenerator
	barIDGenerator *idgenerator.IDGenerator
	urrIDGenerator *idgenerator.IDGenerator
	qerIDGenerator *idgenerator.IDGenerator
}

// SetUPFunctionFeatures replaces the feature list advertised by the UPF.
// Copying keeps PFCP receive buffers out of long-lived SMF context.
func (upf *UPF) SetUPFunctionFeatures(features []byte) {
	upf.upFunctionFeaturesMu.Lock()
	upf.upFunctionFeatures = append(upf.upFunctionFeatures[:0], features...)
	upf.upFunctionFeaturesMu.Unlock()
}

// UPFunctionFeatures returns a snapshot of the UPF's advertised features.
func (upf *UPF) UPFunctionFeatures() []byte {
	upf.upFunctionFeaturesMu.RLock()
	defer upf.upFunctionFeaturesMu.RUnlock()
	return append([]byte(nil), upf.upFunctionFeatures...)
}

// SetRecoveryTimeStamp replaces the UPF recovery-time baseline used by active
// heartbeat restart detection.
func (upf *UPF) SetRecoveryTimeStamp(recoveryTime time.Time) {
	upf.recoveryTimeStampMu.Lock()
	upf.recoveryTimeStamp = recoveryTime
	upf.recoveryTimeStampMu.Unlock()
}

// RecoveryTimeStamp returns the current UPF recovery-time baseline.
func (upf *UPF) RecoveryTimeStamp() time.Time {
	upf.recoveryTimeStampMu.RLock()
	defer upf.recoveryTimeStampMu.RUnlock()
	return upf.recoveryTimeStamp
}

// AcceptRecoveryTimeStamp installs the first heartbeat recovery time and
// atomically compares later values with that baseline. It returns false when
// the UPF advertises a newer timestamp and has therefore restarted.
func (upf *UPF) AcceptRecoveryTimeStamp(recoveryTime time.Time) bool {
	upf.recoveryTimeStampMu.Lock()
	defer upf.recoveryTimeStampMu.Unlock()
	if upf.recoveryTimeStamp.IsZero() {
		upf.recoveryTimeStamp = recoveryTime
		return true
	}
	return !upf.recoveryTimeStamp.Before(recoveryTime)
}

// AcceptRecoveryTimeStampForGeneration applies the heartbeat recovery-time
// comparison only while the association generation observed by the caller is
// still current. The second result reports a newer UPF recovery timestamp.
func (upf *UPF) AcceptRecoveryTimeStampForGeneration(
	generation uint64,
	recoveryTime time.Time,
) (currentGeneration bool, restarted bool) {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	if upf.associationGeneration != generation {
		return false, false
	}

	upf.recoveryTimeStampMu.Lock()
	defer upf.recoveryTimeStampMu.Unlock()
	if upf.recoveryTimeStamp.IsZero() {
		upf.recoveryTimeStamp = recoveryTime
		return true, false
	}
	return true, upf.recoveryTimeStamp.Before(recoveryTime)
}

// AssociationState returns the current node-level PFCP association state.
func (upf *UPF) AssociationState() UPFAssociationState {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	return upf.associationState
}

// AssociationStateAndGeneration returns one consistent lifecycle snapshot for
// work that must not affect a replacement association.
func (upf *UPF) AssociationStateAndGeneration() (UPFAssociationState, uint64) {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	return upf.associationState, upf.associationGeneration
}

// BeginAssociationLifecycle elects one goroutine to own setup, monitoring,
// cleanup and reconnect ion for this configured UPF.
func (upf *UPF) BeginAssociationLifecycle() bool {
	upf.associationMu.Lock()
	defer upf.associationMu.Unlock()
	if upf.associationLifecycle {
		return false
	}
	upf.associationLifecycle = true
	return true
}

// EndAssociationLifecycle releases lifecycle ownership during SMF shutdown or
// when the owner cannot continue.
func (upf *UPF) EndAssociationLifecycle() {
	upf.associationMu.Lock()
	upf.associationLifecycle = false
	upf.associationMu.Unlock()
}

// BeginAssociationSetup moves a disconnected UPF into setup state. It returns
// false when another lifecycle operation already owns the association.
func (upf *UPF) BeginAssociationSetup() bool {
	upf.associationMu.Lock()
	defer upf.associationMu.Unlock()
	if upf.associationState != AssociationDown {
		return false
	}
	upf.associationState = AssociationSettingUp
	return true
}

// FailAssociationSetup returns a setup attempt to the disconnected state.
func (upf *UPF) FailAssociationSetup() {
	upf.associationMu.Lock()
	if upf.associationState == AssociationSettingUp {
		upf.associationState = AssociationDown
	}
	upf.associationMu.Unlock()
}

// EstablishAssociation installs the cancellation context and makes the UPF
// available for PFCP session work. Association state, context and cancellation
// ownership are updated together under one lock.
func (upf *UPF) EstablishAssociation(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	associationContext, cancel := context.WithCancel(parent)

	upf.associationMu.Lock()
	previousCancel := upf.cancelAssociation
	upf.associationGeneration++
	generation := upf.associationGeneration
	upf.associationContext = associationContext
	upf.cancelAssociation = cancel
	upf.associationState = AssociationEstablished
	upf.associationMu.Unlock()

	if previousCancel != nil {
		previousCancel()
	}
	context.AfterFunc(associationContext, func() {
		upf.associationMu.Lock()
		if upf.associationGeneration == generation {
			upf.associationState = AssociationDown
		}
		upf.associationMu.Unlock()
	})
	return associationContext
}

// CancelAssociation synchronously marks the UPF disconnected, clears the
// recovery-time baseline, then wakes goroutines waiting on AssociationDone.
func (upf *UPF) CancelAssociation() {
	upf.associationMu.Lock()
	cancel := upf.cancelAssociationLocked()
	upf.associationMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// CancelAssociationIfGeneration invalidates only the association generation
// observed by the caller. A delayed callback from an older generation is a
// no-op and cannot tear down a replacement association.
func (upf *UPF) CancelAssociationIfGeneration(generation uint64) bool {
	upf.associationMu.Lock()
	if upf.associationGeneration != generation {
		upf.associationMu.Unlock()
		return false
	}
	cancel := upf.cancelAssociationLocked()
	upf.associationMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// cancelAssociationLocked requires associationMu to be held for writing.
func (upf *UPF) cancelAssociationLocked() context.CancelFunc {
	cancel := upf.cancelAssociation
	upf.associationGeneration++
	upf.associationState = AssociationDown
	upf.recoveryTimeStampMu.Lock()
	upf.recoveryTimeStamp = time.Time{}
	upf.recoveryTimeStampMu.Unlock()
	return cancel
}

// AssociationDone exposes only the cancellation signal; association state is
// authoritative for lifecycle decisions.
func (upf *UPF) AssociationDone() <-chan struct{} {
	upf.associationMu.RLock()
	associationContext := upf.associationContext
	upf.associationMu.RUnlock()
	if associationContext == nil || associationContext.Done() == nil {
		return closedAssociationDone
	}
	return associationContext.Done()
}

// AssociationContext returns the lifecycle context for the current
// association. Node and Session transactions should derive cancellation from
// this context so heartbeat loss, Association Release, or SMF shutdown aborts
// them together.
func (upf *UPF) AssociationContext() (context.Context, error) {
	upf.associationMu.RLock()
	state := upf.associationState
	associationContext := upf.associationContext
	upf.associationMu.RUnlock()

	if state != AssociationEstablished && state != AssociationReleasing {
		return nil, fmt.Errorf("UPF[%s] not associated with SMF",
			upf.NodeID.ResolveNodeIdToIp().String())
	}
	if associationContext == nil {
		return nil, fmt.Errorf("UPF[%s] has no association context",
			upf.NodeID.ResolveNodeIdToIp().String())
	}
	if err := associationContext.Err(); err != nil {
		return nil, fmt.Errorf("UPF[%s] association context is canceled: %w",
			upf.NodeID.ResolveNodeIdToIp().String(), err)
	}
	return associationContext, nil
}

// BeginSessionWork reserves the UPF for one PFCP Session Establishment or
// Modification and returns the context of the association being used. The
// finish function must be called on every exit path.
func (upf *UPF) BeginSessionWork() (context.Context, func(), error) {
	upf.sessionWorkGate.RLock()
	associationContext, err := upf.AssociationContext()
	if err != nil {
		upf.sessionWorkGate.RUnlock()
		return nil, nil, err
	}
	if err = upf.IsAvailable(); err != nil {
		upf.sessionWorkGate.RUnlock()
		return nil, nil, err
	}
	return associationContext, upf.sessionWorkGate.RUnlock, nil
}

// BeginAssociationRelease changes Established to Releasing, preventing new
// session work from passing its availability check, and then waits until all
// Establishment/Modification work that already holds a read lock has finished.
func (upf *UPF) BeginAssociationRelease() bool {
	upf.associationMu.Lock()
	if upf.associationState != AssociationEstablished {
		upf.associationMu.Unlock()
		return false
	}
	upf.associationState = AssociationReleasing
	upf.associationMu.Unlock()

	upf.waitForSessionWorkToFinish()
	return true
}

// waitForSessionWorkToFinish is an RWMutex barrier. Acquiring the write lock
// blocks until every BeginSessionWork read lock has been released. No protected
// operation is needed while the write lock is held because AssociationReleasing
// already prevents subsequent session work from being accepted.
func (upf *UPF) waitForSessionWorkToFinish() {
	upf.sessionWorkGate.Lock()
	defer upf.sessionWorkGate.Unlock()
}

func (upf *UPF) IsAssociationReleasing() bool {
	return upf.AssociationState() == AssociationReleasing
}

// IsAvailable permits new PFCP Session Establishment and Modification only on
// a fully established association. Releasing remains associated for reports
// and Session Deletion, but is not available for new session work.
func (upf *UPF) IsAvailable() error {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	return upf.isAvailableLocked()
}

// isAvailableLocked reports whether new work may be added to the current
// association. The caller must hold associationMu for reading or writing.
func (upf *UPF) isAvailableLocked() error {
	if upf.associationState != AssociationEstablished {
		return fmt.Errorf("UPF[%s] is not available for PFCP session work (association state=%s)",
			upf.NodeID.ResolveNodeIdToIp().String(), upf.associationState)
	}
	return nil
}

// ruleAllocationAvailableLocked preserves the historical "not associated"
// error for Down/SettingUp while giving Releasing the stricter availability
// semantics required for new rule allocation.
func (upf *UPF) ruleAllocationAvailableLocked() error {
	if upf.associationState == AssociationEstablished {
		return nil
	}
	if upf.associationState == AssociationReleasing {
		return fmt.Errorf("UPF[%s] is not available for new PFCP rule allocation (association state=%s)",
			upf.NodeID.ResolveNodeIdToIp().String(), upf.associationState)
	}
	return fmt.Errorf("UPF[%s] not associated with SMF",
		upf.NodeID.ResolveNodeIdToIp().String())
}

// InvalidatePFCPSessions clears every remote SEID belonging to this UPF before
// the lifecycle can establish a replacement association. It returns the number
// of live PFCP sessions invalidated.
func (upf *UPF) InvalidatePFCPSessions() int {
	invalidated := 0
	smContextPool.Range(func(_, value interface{}) bool {
		smContext, ok := value.(*SMContext)
		if !ok || smContext == nil {
			return true
		}
		smContext.SMLock.Lock()
		for _, pfcpSession := range smContext.PFCPContext {
			if pfcpSession != nil && pfcpSession.RemoteSEID != 0 &&
				pfcpSession.NodeID.EqualsTo(&upf.NodeID) {
				pfcpSession.RemoteSEID = 0
				invalidated++
			}
		}
		smContext.SMLock.Unlock()
		return true
	})
	return invalidated
}

// CollectAssociationReleasePDUSessions returns each PDU Session that currently
// has a live PFCP session on this UPF. A PDU Session is returned only once even
// if its internal representation contains more than one matching PFCP context.
// Workers re-read mutable PFCP state while holding SMLock instead of relying on
// a stale SEID snapshot collected here.
func (upf *UPF) CollectAssociationReleasePDUSessions() []*SMContext {
	targets := make([]*SMContext, 0)
	smContextPool.Range(func(_, value interface{}) bool {
		smContext, ok := value.(*SMContext)
		if !ok || smContext == nil {
			return true
		}
		smContext.SMLock.Lock()
		for _, pfcpSession := range smContext.PFCPContext {
			if pfcpSession == nil || pfcpSession.RemoteSEID == 0 ||
				!pfcpSession.NodeID.EqualsTo(&upf.NodeID) {
				continue
			}
			targets = append(targets, smContext)
			break
		}
		smContext.SMLock.Unlock()
		return true
	})
	return targets
}

// UPFSelectionParams ... parameters for upf selection
type UPFSelectionParams struct {
	Dnn        string
	SNssai     *SNssai
	Dnai       string
	PDUAddress net.IP
}

// UPFInterfaceInfo store the UPF interface information
type UPFInterfaceInfo struct {
	NetworkInstances      []string
	IPv4EndPointAddresses []net.IP
	IPv6EndPointAddresses []net.IP
	EndpointFQDN          string
}

func GetUpfById(uuid string) *UPF {
	upf, ok := upfPool.Load(uuid)
	if ok {
		return upf.(*UPF)
	}
	return nil
}

// NewUPFInterfaceInfo parse the InterfaceUpfInfoItem to generate UPFInterfaceInfo
func NewUPFInterfaceInfo(i *factory.InterfaceUpfInfoItem) *UPFInterfaceInfo {
	interfaceInfo := new(UPFInterfaceInfo)

	interfaceInfo.IPv4EndPointAddresses = make([]net.IP, 0)
	interfaceInfo.IPv6EndPointAddresses = make([]net.IP, 0)

	logger.CtxLog.Infoln("Endpoints:", i.Endpoints)

	for _, endpoint := range i.Endpoints {
		eIP := net.ParseIP(endpoint)
		if eIP == nil {
			interfaceInfo.EndpointFQDN = endpoint
		} else if eIPv4 := eIP.To4(); eIPv4 == nil {
			interfaceInfo.IPv6EndPointAddresses = append(interfaceInfo.IPv6EndPointAddresses, eIP)
		} else {
			interfaceInfo.IPv4EndPointAddresses = append(interfaceInfo.IPv4EndPointAddresses, eIPv4)
		}
	}

	interfaceInfo.NetworkInstances = make([]string, len(i.NetworkInstances))
	copy(interfaceInfo.NetworkInstances, i.NetworkInstances)

	return interfaceInfo
}

// *** add unit test ***//
// IP returns the IP of the user plane IP information of the pduSessType
func (i *UPFInterfaceInfo) IP(pduSessType uint8) (net.IP, error) {
	if (pduSessType == nasie.PDUSessType_IPv4 ||
		pduSessType == nasie.PDUSessType_IPv4v6) && len(i.IPv4EndPointAddresses) != 0 {
		return i.IPv4EndPointAddresses[0], nil
	}

	if (pduSessType == nasie.PDUSessType_IPv6 ||
		pduSessType == nasie.PDUSessType_IPv4v6) && len(i.IPv6EndPointAddresses) != 0 {
		return i.IPv6EndPointAddresses[0], nil
	}

	if i.EndpointFQDN != "" {
		if resolvedAddr, err := net.ResolveIPAddr("ip", i.EndpointFQDN); err != nil {
			logger.CtxLog.Errorf("resolve addr [%s] failed", i.EndpointFQDN)
		} else {
			switch pduSessType {
			case nasie.PDUSessType_IPv4:
				return resolvedAddr.IP.To4(), nil
			case nasie.PDUSessType_IPv6:
				return resolvedAddr.IP.To16(), nil
			default:
				v4addr := resolvedAddr.IP.To4()
				if v4addr != nil {
					return v4addr, nil
				} else {
					return resolvedAddr.IP.To16(), nil
				}
			}
		}
	}

	return nil, errors.New("not matched ip address")
}

func (upfSelectionParams *UPFSelectionParams) String() string {
	str := ""
	Dnn := upfSelectionParams.Dnn
	if Dnn != "" {
		str += fmt.Sprintf("Dnn: %s\n", Dnn)
	}

	SNssai := upfSelectionParams.SNssai
	if SNssai != nil {
		str += fmt.Sprintf("Sst: %d, Sd: %s\n", int(SNssai.Sst), SNssai.Sd)
	}

	Dnai := upfSelectionParams.Dnai
	if Dnai != "" {
		str += fmt.Sprintf("DNAI: %s\n", Dnai)
	}

	pduAddress := upfSelectionParams.PDUAddress
	if pduAddress != nil {
		str += fmt.Sprintf("PDUAddress: %s\n", pduAddress)
	}

	return str
}

// UUID return this UPF UUID (allocate by SMF in this time)
// Maybe allocate by UPF in future
func (upf *UPF) UUID() string {
	uuid := upf.uuid.String()
	return uuid
}

func NewUPTunnel() (tunnel *UPTunnel) {
	tunnel = &UPTunnel{
		DataPathPool:    make(DataPathPool),
		PathIDGenerator: idgenerator.NewGenerator(1, 2147483647),
	}

	return
}

// *** add unit test ***//
func (t *UPTunnel) AddDataPath(dataPath *DataPath) {
	pathID, err := t.PathIDGenerator.Allocate()
	if err != nil {
		logger.CtxLog.Warnf("Allocate pathID error: %+v", err)
		return
	}

	dataPath.PathID = pathID
	t.DataPathPool[pathID] = dataPath
}

func (t *UPTunnel) RemoveDataPath(pathID int64) {
	delete(t.DataPathPool, pathID)
	t.PathIDGenerator.FreeID(pathID)
}

// *** add unit test ***//
// NewUPF returns a new UPF context in SMF
func NewUPF(nodeID *pfcptype.NodeID, ifaces []*factory.InterfaceUpfInfoItem) (upf *UPF) {
	upf = new(UPF)
	upf.uuid = uuid.New()

	upfPool.Store(upf.UUID(), upf)

	upf.associationState = AssociationDown

	upf.NodeID = *nodeID
	upf.pdrIDGenerator = idgenerator.NewGenerator(1, math.MaxUint16)
	upf.farIDGenerator = idgenerator.NewGenerator(1, math.MaxUint32)
	upf.barIDGenerator = idgenerator.NewGenerator(1, math.MaxUint8)
	upf.qerIDGenerator = idgenerator.NewGenerator(1, math.MaxUint32)
	upf.urrIDGenerator = idgenerator.NewGenerator(1, math.MaxUint32)

	upf.N3Interfaces = make([]*UPFInterfaceInfo, 0)
	upf.N9Interfaces = make([]*UPFInterfaceInfo, 0)

	for _, iface := range ifaces {
		upIface := NewUPFInterfaceInfo(iface)

		switch iface.InterfaceType {
		case models.Nrf_NFMgmt_UPInterfaceType_N3:
			upf.N3Interfaces = append(upf.N3Interfaces, upIface)
		case models.Nrf_NFMgmt_UPInterfaceType_N9:
			upf.N9Interfaces = append(upf.N9Interfaces, upIface)
		}
	}

	return upf
}

// *** add unit test ***//
// GetInterface return the UPFInterfaceInfo that match input cond
func (upf *UPF) GetInterface(interfaceType models.Nrf_NFMgmt_UPInterfaceType, dnn string) *UPFInterfaceInfo {
	switch interfaceType {
	case models.Nrf_NFMgmt_UPInterfaceType_N3:
		for i, iface := range upf.N3Interfaces {
			for _, nwInst := range iface.NetworkInstances {
				if nwInst == dnn {
					return upf.N3Interfaces[i]
				}
			}
		}
	case models.Nrf_NFMgmt_UPInterfaceType_N9:
		for i, iface := range upf.N9Interfaces {
			for _, nwInst := range iface.NetworkInstances {
				if nwInst == dnn {
					return upf.N9Interfaces[i]
				}
			}
		}
	}
	return nil
}

func (upf *UPF) PFCPAddr() *net.UDPAddr {
	return &net.UDPAddr{
		IP:   upf.NodeID.ResolveNodeIdToIp(),
		Port: 8805,
	}
}

// *** add unit test ***//
func RetrieveUPFNodeByNodeID(nodeID pfcptype.NodeID) *UPF {
	var targetUPF *UPF = nil
	upfPool.Range(func(key, value interface{}) bool {
		curUPF := value.(*UPF)
		if curUPF.NodeID.NodeIdType != nodeID.NodeIdType &&
			(curUPF.NodeID.NodeIdType == pfcptype.NodeIdTypeFqdn || nodeID.NodeIdType == pfcptype.NodeIdTypeFqdn) {
			curUPFNodeIdIP := curUPF.NodeID.ResolveNodeIdToIp().To4()
			nodeIdIP := nodeID.ResolveNodeIdToIp().To4()
			logger.CtxLog.Tracef("RetrieveUPF - upfNodeIdIP:[%+v], nodeIdIP:[%+v]", curUPFNodeIdIP, nodeIdIP)
			if reflect.DeepEqual(curUPFNodeIdIP, nodeIdIP) {
				targetUPF = curUPF
				return false
			}
		} else if reflect.DeepEqual(curUPF.NodeID, nodeID) {
			targetUPF = curUPF
			return false
		}
		return true
	})

	return targetUPF
}

// *** add unit test ***//
func RemoveUPFNodeByNodeID(nodeID pfcptype.NodeID) bool {
	upfID := ""
	upfPool.Range(func(key, value interface{}) bool {
		upfID = key.(string)
		upf := value.(*UPF)
		if upf.NodeID.NodeIdType != nodeID.NodeIdType &&
			(upf.NodeID.NodeIdType == pfcptype.NodeIdTypeFqdn || nodeID.NodeIdType == pfcptype.NodeIdTypeFqdn) {
			upfNodeIdIP := upf.NodeID.ResolveNodeIdToIp().To4()
			nodeIdIP := nodeID.ResolveNodeIdToIp().To4()
			logger.CtxLog.Tracef("RemoveUPF - upfNodeIdIP:[%+v], nodeIdIP:[%+v]", upfNodeIdIP, nodeIdIP)
			if reflect.DeepEqual(upfNodeIdIP, nodeIdIP) {
				return false
			}
		} else if reflect.DeepEqual(upf.NodeID, nodeID) {
			return false
		}
		upfID = ""
		return true
	})

	if upfID != "" {
		upfPool.Delete(upfID)
		return true
	}
	return false
}

func (upf *UPF) GetUPFIP() string {
	upfIP := upf.NodeID.ResolveNodeIdToIp().String()
	return upfIP
}

func (upf *UPF) GetUPFID() string {
	upInfo := GetUserPlaneInformation()
	upfIP := upf.NodeID.ResolveNodeIdToIp().String()
	return upInfo.GetUPFIDByIP(upfIP)
}

func (upf *UPF) pdrID() (pdrID uint16, err error) {
	tmpID, err := upf.pdrIDGenerator.Allocate()
	if err != nil {
		return 0, err
	}
	pdrID = uint16(tmpID)
	return
}

func (upf *UPF) farID() (farID uint32, err error) {
	tmpID, err := upf.farIDGenerator.Allocate()
	if err != nil {
		return 0, err
	}
	farID = uint32(tmpID)
	return
}

func (upf *UPF) barID() (barID uint8, err error) {
	tmpID, err := upf.barIDGenerator.Allocate()
	if err != nil {
		return 0, err
	}
	barID = uint8(tmpID)
	return
}

func (upf *UPF) qerID() (qerID uint32, err error) {
	tmpID, err := upf.qerIDGenerator.Allocate()
	if err != nil {
		return 0, err
	}
	qerID = uint32(tmpID)
	return
}

func (upf *UPF) urrID() (urrID uint32, err error) {
	tmpID, err := upf.urrIDGenerator.Allocate()
	if err != nil {
		return 0, err
	}
	urrID = uint32(tmpID)
	return
}

func (upf *UPF) AddPDR() (pdr *PDR, err error) {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	if err = upf.ruleAllocationAvailableLocked(); err != nil {
		return
	}
	return upf.addPDR()
}

// addPDR allocates a PDR and its FAR for work that already owns association
// availability (for example, a BeginSessionWork reservation). It deliberately
// does not acquire sessionWorkGate, avoiding recursive RWMutex read locking.
func (upf *UPF) addPDR() (pdr *PDR, err error) {
	pdrID, err := upf.pdrID()
	if err != nil {
		return
	}

	newFAR, err := upf.addFAR()
	if err != nil {
		upf.pdrIDGenerator.FreeID(int64(pdrID))
		return
	}

	pdr = &PDR{
		PDRID: pdrID,
		FAR:   newFAR,
	}
	upf.pdrPool.Store(pdr.PDRID, pdr)
	return
}

func (upf *UPF) AddFAR() (far *FAR, err error) {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	if err = upf.ruleAllocationAvailableLocked(); err != nil {
		return
	}
	return upf.addFAR()
}

func (upf *UPF) addFAR() (far *FAR, err error) {
	farID, err := upf.farID()
	if err != nil {
		return
	}
	far = &FAR{
		FARID: farID,
	}
	upf.farPool.Store(far.FARID, far)
	return far, nil
}

func (upf *UPF) AddBAR() (bar *BAR, err error) {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	if err = upf.ruleAllocationAvailableLocked(); err != nil {
		return
	}

	barID, err := upf.barID()
	if err != nil {
		return
	}
	bar = &BAR{
		BARID: barID,
	}
	upf.barPool.Store(bar.BARID, bar)
	return
}

func (upf *UPF) AddQER() (qer *QER, err error) {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	if err = upf.ruleAllocationAvailableLocked(); err != nil {
		return
	}

	qerID, err := upf.qerID()
	if err != nil {
		return
	}
	qer = &QER{
		QERID: qerID,
	}
	upf.qerPool.Store(qer.QERID, qer)
	return
}

func (upf *UPF) AddURR(urrID uint32, opts ...UrrOpt) (urr *URR, err error) {
	upf.associationMu.RLock()
	defer upf.associationMu.RUnlock()
	if err = upf.ruleAllocationAvailableLocked(); err != nil {
		return
	}

	if urrID == 0 {
		urrID, err = upf.urrID()
		if err != nil {
			return
		}
	}

	urr = &URR{
		URRID:                  urrID,
		MeasureMethod:          MesureMethodVol,
		MeasurementInformation: MeasureInformation(true, false),
	}

	for _, opt := range opts {
		opt(urr)
	}

	upf.urrPool.Store(urr.URRID, urr)
	return
}

// discardPDRAndFAR rolls back a PDR/FAR pair that has not been published to a
// session. It must remain usable after Association Release changes the state to
// Releasing (or an independent failure cancels the association).
func (upf *UPF) discardPDRAndFAR(pdr *PDR) {
	if pdr == nil {
		return
	}
	upf.pdrPool.Delete(pdr.PDRID)
	upf.pdrIDGenerator.FreeID(int64(pdr.PDRID))
	if pdr.FAR != nil {
		upf.farPool.Delete(pdr.FAR.FARID)
		upf.farIDGenerator.FreeID(int64(pdr.FAR.FARID))
	}
}

func (upf *UPF) GetUUID() uuid.UUID {
	return upf.uuid
}

func (upf *UPF) GetQERById(qerId uint32) *QER {
	qer, ok := upf.qerPool.Load(qerId)
	if ok {
		return qer.(*QER)
	}
	return nil
}

// *** add unit test ***//
func (upf *UPF) RemovePDR(pdr *PDR) (err error) {
	if err = upf.IsAssociated(); err != nil {
		return
	}

	upf.pdrIDGenerator.FreeID(int64(pdr.PDRID))
	upf.pdrPool.Delete(pdr.PDRID)
	return
}

// *** add unit test ***//
func (upf *UPF) RemoveFAR(far *FAR) (err error) {
	if err = upf.IsAssociated(); err != nil {
		return
	}

	upf.farIDGenerator.FreeID(int64(far.FARID))
	upf.farPool.Delete(far.FARID)
	return
}

// *** add unit test ***//
func (upf *UPF) RemoveBAR(bar *BAR) (err error) {
	if err = upf.IsAssociated(); err != nil {
		return
	}

	upf.barIDGenerator.FreeID(int64(bar.BARID))
	upf.barPool.Delete(bar.BARID)
	return
}

// *** add unit test ***//
func (upf *UPF) RemoveQER(qer *QER) (err error) {
	if err = upf.IsAssociated(); err != nil {
		return
	}

	upf.qerIDGenerator.FreeID(int64(qer.QERID))
	upf.qerPool.Delete(qer.QERID)
	return
}

// *** add unit test ***//
func (upf *UPF) RemoveURR(urr *URR) (err error) {
	if err = upf.IsAssociated(); err != nil {
		return
	}

	upf.urrIDGenerator.FreeID(int64(urr.URRID))
	upf.urrPool.Delete(urr.URRID)
	return
}

func (upf *UPF) isSupportSnssai(snssai *SNssai) bool {
	for _, snssaiInfo := range upf.SNssaiInfos {
		if snssaiInfo.SNssai.Equal(snssai) {
			return true
		}
	}
	return false
}

func (upf *UPF) ProcEachSMContext(procFunc func(*SMContext)) {
	smContextPool.Range(func(_, value interface{}) bool {
		smContext, ok := value.(*SMContext)
		if !ok || smContext == nil {
			return true
		}
		smContext.SMLock.Lock()
		affected := false
		for _, pfcpSession := range smContext.PFCPContext {
			if pfcpSession != nil && pfcpSession.NodeID.EqualsTo(&upf.NodeID) {
				affected = true
				break
			}
		}
		smContext.SMLock.Unlock()
		if affected {
			procFunc(smContext)
		}
		return true
	})
}

func (upf *UPF) IsAssociated() error {
	state := upf.AssociationState()
	if state == AssociationEstablished || state == AssociationReleasing {
		return nil
	}
	return fmt.Errorf("UPF[%s] not associated with SMF",
		upf.NodeID.ResolveNodeIdToIp().String())
}

func (state UPFAssociationState) String() string {
	switch state {
	case AssociationDown:
		return "down"
	case AssociationSettingUp:
		return "setting-up"
	case AssociationEstablished:
		return "established"
	case AssociationReleasing:
		return "releasing"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}
