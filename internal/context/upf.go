package context

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/pfcp/pfcpUdp"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

var upfPool sync.Map

type UPFStatus int

const (
	NotAssociated          UPFStatus = 0
	AssociatedSettingUp    UPFStatus = 1
	AssociatedSetUpSuccess UPFStatus = 2
)

type UPF struct {
	*UPNode

	UPIPInfo          pfcpType.UserPlaneIPResourceInformation
	UPFStatus         UPFStatus
	RecoveryTimeStamp time.Time

	PFCPSessionContexts map[uint64]*PFCPSessionContext //localSEID to PFCPSessionContext

	RestoresSessions           context.Context
	RestoresSessionsCancelFunc context.CancelFunc

	Association           context.Context
	AssociationCancelFunc context.CancelFunc

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

func (upf *UPF) String() string {
	str := "UPF {\n"
	prefix := "  "
	str += prefix + fmt.Sprintf("Name: %s\n", upf.Name)
	str += prefix + fmt.Sprintf("ID: %s\n", upf.ID)
	str += prefix + fmt.Sprintf("NodeID: %s\n", upf.GetNodeIDString())
	str += prefix + fmt.Sprintf("Dnn: %s\n", upf.Dnn)
	str += prefix + fmt.Sprintln("Links:")
	for _, link := range upf.Links {
		str += prefix + fmt.Sprintf("-- %s: %s\n", link.GetName(), link.GetNodeIDString())
	}
	str += prefix + fmt.Sprintf("N3Interfaces: %s\n", upf.N3Interfaces)
	str += "}"
	return str
}

// Checks the NodeID type and either returns IPv4, IPv6, or FQDN
func (upf *UPF) GetNodeIDString() string {
	switch upf.NodeID.NodeIdType {
	case pfcpType.NodeIdTypeIpv4Address, pfcpType.NodeIdTypeIpv6Address:
		return upf.NodeID.IP.String()
	case pfcpType.NodeIdTypeFqdn:
		return upf.NodeID.FQDN
	default:
		logger.CtxLog.Errorf("nodeID has unknown type %d", upf.NodeID.NodeIdType)
		return ""
	}
}

func (upf *UPF) GetNodeID() pfcpType.NodeID {
	return upf.NodeID
}

func (upf *UPF) GetName() string {
	return upf.Name
}

func (upf *UPF) GetID() uuid.UUID {
	return upf.ID
}

func (upf *UPF) GetType() UPNodeType {
	return upf.Type
}

func (upf *UPF) GetLinks() UPPath {
	return upf.Links
}

func (upf *UPF) AddLink(link UPNodeInterface) bool {
	for _, existingLink := range upf.Links {
		if link.GetName() == existingLink.GetName() {
			logger.CfgLog.Warningf("UPLink [%s] <=> [%s] already exists, skip\n", existingLink.GetName(), link.GetName())
			return false
		}
	}
	upf.Links = append(upf.Links, link)
	return true
}

func (upf *UPF) RemoveLink(link UPNodeInterface) bool {
	for i, existingLink := range upf.Links {
		if link.GetName() == existingLink.GetName() && existingLink.GetNodeIDString() == link.GetNodeIDString() {
			logger.CfgLog.Warningf("Remove UPLink [%s] <=> [%s]\n", existingLink.GetName(), link.GetName())
			upf.Links = append(upf.Links[:i], upf.Links[i+1:]...)
			return true
		}
	}
	return false
}

// UPFSelectionParams ... parameters for upf selection
type UPFSelectionParams struct {
	Dnn        string
	SNssai     *SNssai
	Dnai       string
	PDUAddress net.IP
}

func (upfSelectionParams *UPFSelectionParams) String() string {
	str := "UPFSelectionParams {"
	if upfSelectionParams.Dnn != "" {
		str += fmt.Sprintf("Dnn: %s -- ", upfSelectionParams.Dnn)
	}
	if upfSelectionParams.SNssai != nil {
		str += fmt.Sprintf("Sst: %d, Sd: %s -- ", upfSelectionParams.SNssai.Sst, upfSelectionParams.SNssai.Sd)
	}
	if upfSelectionParams.Dnai != "" {
		str += fmt.Sprintf("DNAI: %s -- ", upfSelectionParams.Dnai)
	}
	if upfSelectionParams.PDUAddress != nil {
		str += fmt.Sprintf("PDUAddress: %s -- ", upfSelectionParams.PDUAddress)
	}
	str += " }"
	return str
}

// UPFInterfaceInfo store the UPF interface information
type UPFInterfaceInfo struct {
	NetworkInstances      []string
	IPv4EndPointAddresses []net.IP
	IPv6EndPointAddresses []net.IP
	EndpointFQDN          string
}

func (i *UPFInterfaceInfo) String() string {
	str := ""
	str += fmt.Sprintf("NetworkInstances: %v, ", i.NetworkInstances)
	str += fmt.Sprintf("IPv4EndPointAddresses: %v, ", i.IPv4EndPointAddresses)
	str += fmt.Sprintf("EndpointFQDN: %s", i.EndpointFQDN)
	return str
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
	if (pduSessType == nasMessage.PDUSessionTypeIPv4 ||
		pduSessType == nasMessage.PDUSessionTypeIPv4IPv6) && len(i.IPv4EndPointAddresses) != 0 {
		return i.IPv4EndPointAddresses[0], nil
	}

	if (pduSessType == nasMessage.PDUSessionTypeIPv6 ||
		pduSessType == nasMessage.PDUSessionTypeIPv4IPv6) && len(i.IPv6EndPointAddresses) != 0 {
		return i.IPv6EndPointAddresses[0], nil
	}

	if i.EndpointFQDN != "" {
		if resolvedAddr, err := net.ResolveIPAddr("ip", i.EndpointFQDN); err != nil {
			logger.CtxLog.Errorf("resolve addr [%s] failed", i.EndpointFQDN)
		} else {
			if pduSessType == nasMessage.PDUSessionTypeIPv4 {
				return resolvedAddr.IP.To4(), nil
			} else if pduSessType == nasMessage.PDUSessionTypeIPv6 {
				return resolvedAddr.IP.To16(), nil
			} else {
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

// *** add unit test ***//
// NewUPF returns a new UPF context in SMF
func NewUPF(
	upNode *UPNode,
	ifaces []*factory.InterfaceUpfInfoItem,
	upfSNssaiInfos []*factory.SnssaiUpfInfoItem,
) (upf *UPF) {
	upf = new(UPF)
	upf.UPNode = upNode

	// Initialize context
	// Initialize context
	upf.UPFStatus = NotAssociated
	upf.PFCPSessionContexts = make(map[uint64]*PFCPSessionContext)

	// init association readiness signal,
	// but immediately cancel contexts to avoid nil reference
	upf.Association, upf.AssociationCancelFunc = context.WithCancel(context.Background())
	upf.AssociationCancelFunc()

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
		case models.UpInterfaceType_N3:
			upf.N3Interfaces = append(upf.N3Interfaces, upIface)
		case models.UpInterfaceType_N9:
			upf.N9Interfaces = append(upf.N9Interfaces, upIface)
		}
	}

	snssaiInfos := make([]*SnssaiUPFInfo, 0)
	for _, snssaiInfoConfig := range upfSNssaiInfos {
		snssaiInfo := SnssaiUPFInfo{
			SNssai: &SNssai{
				Sst: snssaiInfoConfig.SNssai.Sst,
				Sd:  snssaiInfoConfig.SNssai.Sd,
			},
			DnnList: make([]*DnnUPFInfoItem, 0),
		}

		for _, dnnInfoConfig := range snssaiInfoConfig.DnnUpfInfoList {
			ueIPPools := make([]*UeIPPool, 0)
			staticUeIPPools := make([]*UeIPPool, 0)
			for _, pool := range dnnInfoConfig.Pools {
				ueIPPool := NewUEIPPool(pool)
				if ueIPPool == nil {
					logger.InitLog.Fatalf("invalid pools value: %+v", pool)
				} else {
					ueIPPools = append(ueIPPools, ueIPPool)
				}
			}
			for _, staticPool := range dnnInfoConfig.StaticPools {
				staticUeIPPool := NewUEIPPool(staticPool)
				if staticUeIPPool == nil {
					logger.InitLog.Fatalf("invalid pools value: %+v", staticPool)
				} else {
					staticUeIPPools = append(staticUeIPPools, staticUeIPPool)
					for _, dynamicUePool := range ueIPPools {
						if dynamicUePool.ueSubNet.Contains(staticUeIPPool.ueSubNet.IP) {
							if err := dynamicUePool.Exclude(staticUeIPPool); err != nil {
								logger.InitLog.Fatalf("exclude static Pool[%s] failed: %v",
									staticUeIPPool.ueSubNet, err)
							}
						}
					}
				}
			}
			for _, pool := range ueIPPools {
				if pool.pool.Min() != pool.pool.Max() {
					if err := pool.pool.Reserve(pool.pool.Min(), pool.pool.Min()); err != nil {
						logger.InitLog.Errorf("Remove network address failed for %s: %s", pool.ueSubNet.String(), err)
					}
					if err := pool.pool.Reserve(pool.pool.Max(), pool.pool.Max()); err != nil {
						logger.InitLog.Errorf("Remove network address failed for %s: %s", pool.ueSubNet.String(), err)
					}
				}
				logger.InitLog.Debugf("%d-%s %s %s",
					snssaiInfo.SNssai.Sst, snssaiInfo.SNssai.Sd,
					dnnInfoConfig.Dnn, pool.dump())
			}
			snssaiInfo.DnnList = append(snssaiInfo.DnnList, &DnnUPFInfoItem{
				Dnn:             dnnInfoConfig.Dnn,
				DnaiList:        dnnInfoConfig.DnaiList,
				PduSessionTypes: dnnInfoConfig.PduSessionTypes,
				UeIPPools:       ueIPPools,
				StaticIPPools:   staticUeIPPools,
			})
		}
		snssaiInfos = append(snssaiInfos, &snssaiInfo)
	}
	upf.SNssaiInfos = snssaiInfos

	logger.CtxLog.Debugf("Created UPF context %s\n", upf)

	return upf
}

// *** add unit test ***//
// GetInterface return the UPFInterfaceInfo that match input cond
func (upf *UPF) GetInterface(interfaceType models.UpInterfaceType, dnn string) *UPFInterfaceInfo {
	switch interfaceType {
	case models.UpInterfaceType_N3:
		if len(upf.N3Interfaces) == 0 {
			logger.CtxLog.Warnf("UPF[%s] has no known N3 interfaces!", upf.GetNodeIDString())
		} else {
			logger.CtxLog.Tracef("Getting N3 interface for UPF[%s] for DNN %s", upf.GetNodeIDString(), dnn)
			for i, iface := range upf.N3Interfaces {
				logger.CtxLog.Tracef("UPF[%s] has interface %s", upf.GetNodeIDString(), iface)
				for _, nwInst := range iface.NetworkInstances {
					if nwInst == dnn {
						return upf.N3Interfaces[i]
					}
				}
			}
		}
	case models.UpInterfaceType_N9:
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
		IP:   net.IP(upf.GetNodeIDString()),
		Port: pfcpUdp.PFCP_PORT,
	}
}

func (upf *UPF) generatePDRID() (uint16, error) {
	select {
	case <-upf.Association.Done():
		return 0, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	if tmpID, err := upf.pdrIDGenerator.Allocate(); err != nil {
		return 0, err
	} else {
		return uint16(tmpID), nil
	}
}

func (upf *UPF) generateFARID() (uint32, error) {
	select {
	case <-upf.Association.Done():
		err := fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
		return 0, err
	default:
	}

	if tmpID, err := upf.farIDGenerator.Allocate(); err != nil {
		return 0, err
	} else {
		return uint32(tmpID), nil
	}
}

func (upf *UPF) generateBARID() (uint8, error) {
	select {
	case <-upf.Association.Done():
		return 0, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	if tmpID, err := upf.barIDGenerator.Allocate(); err != nil {
		return 0, err
	} else {
		return uint8(tmpID), nil
	}
}

func (upf *UPF) generateQERID() (uint32, error) {
	select {
	case <-upf.Association.Done():
		return 0, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	if tmpID, err := upf.qerIDGenerator.Allocate(); err != nil {
		return 0, err
	} else {
		return uint32(tmpID), nil
	}
}

func (upf *UPF) generateURRID() (uint32, error) {
	select {
	case <-upf.Association.Done():
		return 0, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	if tmpID, err := upf.urrIDGenerator.Allocate(); err != nil {
		return 0, err
	} else {
		return uint32(tmpID), nil
	}
}

func (upf *UPF) AddPDR() (*PDR, error) {
	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	pdr := new(PDR)
	if PDRID, err := upf.generatePDRID(); err != nil {
		return nil, err
	} else {
		pdr.SetState(RULE_INITIAL)
		pdr.PDRID = PDRID
		upf.pdrPool.Store(pdr.PDRID, pdr)
	}

	if far, err := upf.AddFAR(); err != nil {
		return nil, err
	} else {
		pdr.FAR = far
	}

	return pdr, nil
}

func (upf *UPF) AddFAR() (*FAR, error) {
	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	far := new(FAR)
	if FARID, err := upf.generateFARID(); err != nil {
		return nil, err
	} else {
		far.SetState(RULE_INITIAL)
		far.FARID = FARID
		upf.farPool.Store(far.FARID, far)
	}

	return far, nil
}

func (upf *UPF) AddBAR() (*BAR, error) {
	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	bar := new(BAR)
	if BARID, err := upf.generateBARID(); err != nil {
	} else {
		bar.SetState(RULE_INITIAL)
		bar.BARID = BARID
		upf.barPool.Store(bar.BARID, bar)
	}

	return bar, nil
}

func (upf *UPF) AddQER() (*QER, error) {
	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	qer := new(QER)
	if QERID, err := upf.generateQERID(); err != nil {
	} else {
		qer.SetState(RULE_INITIAL)
		qer.QERID = QERID
		upf.qerPool.Store(qer.QERID, qer)
	}

	return qer, nil
}

func (upf *UPF) AddURR(urrId uint32, opts ...UrrOpt) (*URR, error) {
	select {
	case <-upf.Association.Done():
		return nil, fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	urr := new(URR)
	urr.MeasureMethod = MesureMethodVol
	urr.MeasurementInformation = MeasureInformation(true, false)

	for _, opt := range opts {
		opt(urr)
	}

	if urrId == 0 {
		if URRID, err := upf.generateURRID(); err != nil {
		} else {
			urr.SetState(RULE_INITIAL)
			urr.URRID = URRID
			upf.urrPool.Store(urr.URRID, urr)
		}
	} else {
		urr.SetState(RULE_INITIAL)
		urr.URRID = urrId
		upf.urrPool.Store(urr.URRID, urr)
	}
	return urr, nil
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
	select {
	case <-upf.Association.Done():
		return fmt.Errorf("UPF[%s] not associated with SMF", upf.GetNodeIDString())
	default:
	}

	if pdr.FAR != nil {
		if pdr.FAR.BAR != nil {
			upf.barIDGenerator.FreeID(int64(pdr.FAR.BAR.BARID))
			upf.barPool.Delete(pdr.FAR.BAR.BARID)
		}
		upf.farIDGenerator.FreeID(int64(pdr.FAR.FARID))
		upf.farPool.Delete(pdr.FAR.FARID)
	}

	for _, urr := range pdr.URR {
		upf.qerIDGenerator.FreeID(int64(urr.URRID))
		upf.qerPool.Delete(urr.URRID)
	}

	for _, qer := range pdr.QER {
		upf.qerIDGenerator.FreeID(int64(qer.QERID))
		upf.qerPool.Delete(qer.QERID)
	}

	upf.pdrIDGenerator.FreeID(int64(pdr.PDRID))
	upf.pdrPool.Delete(pdr.PDRID)

	return nil
}

func (upf *UPF) isSupportSnssai(snssai *SNssai) bool {
	for _, snssaiInfo := range upf.SNssaiInfos {
		if snssaiInfo.SNssai.Equal(snssai) {
			return true
		}
	}
	return false
}

func (upf *UPF) MatchedSelection(selection *UPFSelectionParams) bool {
	for _, snssaiInfo := range upf.SNssaiInfos {
		currentSnssai := snssaiInfo.SNssai
		if currentSnssai.Equal(selection.SNssai) {
			for _, dnnInfo := range snssaiInfo.DnnList {
				if dnnInfo.Dnn == selection.Dnn {
					if selection.Dnai == "" {
						return true
					} else if dnnInfo.ContainsDNAI(selection.Dnai) {
						return true
					}
				}
			}
		}
	}
	return false
}
