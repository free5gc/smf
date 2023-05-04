package context

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/util"
	"github.com/free5gc/smf/pkg/factory"
)

// Refer to TS 23.501 5.7.4
var standardGbr5QIs = map[int32]struct{}{
	1:  {},
	2:  {},
	3:  {},
	4:  {},
	65: {},
	66: {},
	67: {},
	75: {},
	71: {},
	72: {},
	73: {},
	74: {},
	76: {},
}

// GTPTunnel represents the GTP tunnel information
type GTPTunnel struct {
	SrcEndPoint  *DataPathNode
	DestEndPoint *DataPathNode

	TEID uint32
	PDR  *PDR
}

type DataPathNode struct {
	UPF *UPF
	// DataPathToAN *DataPathDownLink
	// DataPathToDN map[string]*DataPathUpLink //uuid to DataPathLink

	UpLinkTunnel   *GTPTunnel
	DownLinkTunnel *GTPTunnel
	// for UE Routing Topology
	// for special case:
	// branching & leafnode

	// InUse                bool
	IsBranchingPoint bool
	// DLDataPathLinkForPSA *DataPathUpLink
	// BPUpLinkPDRs         map[string]*DataPathDownLink // uuid to UpLink
}

type DataPath struct {
	PathID int64
	// meta data
	Activated         bool
	IsDefaultPath     bool
	GBRFlow           bool
	Destination       Destination
	HasBranchingPoint bool
	// Data Path Double Link List
	FirstDPNode *DataPathNode
}

type DataPathPool map[int64]*DataPath

type Destination struct {
	DestinationIP   string
	DestinationPort string
	Url             string
}

func NewDataPathNode() *DataPathNode {
	node := &DataPathNode{
		UpLinkTunnel:   &GTPTunnel{},
		DownLinkTunnel: &GTPTunnel{},
	}
	return node
}

func NewDataPath() *DataPath {
	dataPath := &DataPath{
		Destination: Destination{
			DestinationIP:   "",
			DestinationPort: "",
			Url:             "",
		},
	}

	return dataPath
}

func NewDataPathPool() DataPathPool {
	pool := make(map[int64]*DataPath)
	return pool
}

func (node *DataPathNode) AddNext(next *DataPathNode) {
	node.DownLinkTunnel.SrcEndPoint = next
}

func (node *DataPathNode) AddPrev(prev *DataPathNode) {
	node.UpLinkTunnel.SrcEndPoint = prev
}

func (node *DataPathNode) Next() *DataPathNode {
	if node.DownLinkTunnel == nil {
		return nil
	}
	next := node.DownLinkTunnel.SrcEndPoint
	return next
}

func (node *DataPathNode) Prev() *DataPathNode {
	if node.UpLinkTunnel == nil {
		return nil
	}
	prev := node.UpLinkTunnel.SrcEndPoint
	return prev
}

func (node *DataPathNode) ActivateUpLinkTunnel(smContext *SMContext) error {
	var err error
	logger.CtxLog.Traceln("In ActivateUpLinkTunnel")
	node.UpLinkTunnel.SrcEndPoint = node.Prev()
	node.UpLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	if node.UpLinkTunnel.PDR, err = destUPF.AddPDR(); err != nil {
		logger.CtxLog.Errorln("In ActivateUpLinkTunnel UPF IP: ", node.UPF.NodeID.ResolveNodeIdToIp().String())
		logger.CtxLog.Errorln("Allocate PDR Error: ", err)
		return fmt.Errorf("Add PDR failed: %s", err)
	}

	if err = smContext.PutPDRtoPFCPSession(destUPF.NodeID, node.UpLinkTunnel.PDR); err != nil {
		logger.CtxLog.Errorln("Put PDR Error: ", err)
		return err
	}

	if teid, err := destUPF.GenerateTEID(); err != nil {
		logger.CtxLog.Errorf("Generate uplink TEID fail: %s", err)
		return err
	} else {
		node.UpLinkTunnel.TEID = teid
	}

	return nil
}

func (node *DataPathNode) ActivateDownLinkTunnel(smContext *SMContext) error {
	var err error
	node.DownLinkTunnel.SrcEndPoint = node.Next()
	node.DownLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	if node.DownLinkTunnel.PDR, err = destUPF.AddPDR(); err != nil {
		logger.CtxLog.Errorln("In ActivateDownLinkTunnel UPF IP: ", node.UPF.NodeID.ResolveNodeIdToIp().String())
		logger.CtxLog.Errorln("Allocate PDR Error: ", err)
		return fmt.Errorf("Add PDR failed: %s", err)
	}

	if err = smContext.PutPDRtoPFCPSession(destUPF.NodeID, node.DownLinkTunnel.PDR); err != nil {
		logger.CtxLog.Errorln("Put PDR Error: ", err)
		return err
	}

	if teid, err := destUPF.GenerateTEID(); err != nil {
		logger.CtxLog.Errorf("Generate downlink TEID fail: %s", err)
		return err
	} else {
		node.DownLinkTunnel.TEID = teid
	}

	return nil
}

func (node *DataPathNode) DeactivateUpLinkTunnel(smContext *SMContext) {
	if pdr := node.UpLinkTunnel.PDR; pdr != nil {
		smContext.RemovePDRfromPFCPSession(node.UPF.NodeID, pdr)
		err := node.UPF.RemovePDR(pdr)
		if err != nil {
			logger.CtxLog.Warnln("Deactivated UpLinkTunnel", err)
		}

		if far := pdr.FAR; far != nil {
			err = node.UPF.RemoveFAR(far)
			if err != nil {
				logger.CtxLog.Warnln("Deactivated UpLinkTunnel", err)
			}

			bar := far.BAR
			if bar != nil {
				err = node.UPF.RemoveBAR(bar)
				if err != nil {
					logger.CtxLog.Warnln("Deactivated UpLinkTunnel", err)
				}
			}
		}
		if qerList := pdr.QER; qerList != nil {
			for _, qer := range qerList {
				if qer != nil {
					err = node.UPF.RemoveQER(qer)
					if err != nil {
						logger.CtxLog.Warnln("Deactivated UpLinkTunnel", err)
					}
				}
			}
		}
	}

	teid := node.UpLinkTunnel.TEID
	node.UPF.teidGenerator.FreeID(int64(teid))
}

func (node *DataPathNode) DeactivateDownLinkTunnel(smContext *SMContext) {
	if pdr := node.DownLinkTunnel.PDR; pdr != nil {
		smContext.RemovePDRfromPFCPSession(node.UPF.NodeID, pdr)
		err := node.UPF.RemovePDR(pdr)
		if err != nil {
			logger.CtxLog.Warnln("Deactivated DownLinkTunnel", err)
		}

		if far := pdr.FAR; far != nil {
			err = node.UPF.RemoveFAR(far)
			if err != nil {
				logger.CtxLog.Warnln("Deactivated DownLinkTunnel", err)
			}

			bar := far.BAR
			if bar != nil {
				err = node.UPF.RemoveBAR(bar)
				if err != nil {
					logger.CtxLog.Warnln("Deactivated DownLinkTunnel", err)
				}
			}
		}
		if qerList := pdr.QER; qerList != nil {
			for _, qer := range qerList {
				if qer != nil {
					err = node.UPF.RemoveQER(qer)
					if err != nil {
						logger.CtxLog.Warnln("Deactivated UpLinkTunnel", err)
					}
				}
			}
		}
	}

	teid := node.DownLinkTunnel.TEID
	node.UPF.teidGenerator.FreeID(int64(teid))
}

func (node *DataPathNode) GetUPFID() (id string, err error) {
	node_ip := node.GetNodeIP()
	var exist bool

	if id, exist = smfContext.UserPlaneInformation.UPFsIPtoID[node_ip]; !exist {
		err = fmt.Errorf("UPNode IP %s doesn't exist in smfcfg.yaml", node_ip)
		return "", err
	}

	return id, nil
}

func (node *DataPathNode) GetNodeIP() (ip string) {
	ip = node.UPF.NodeID.ResolveNodeIdToIp().String()
	return
}

func (node *DataPathNode) IsANUPF() bool {
	if node.Prev() == nil {
		return true
	} else {
		return false
	}
}

func (node *DataPathNode) IsAnchorUPF() bool {
	if node.Next() == nil {
		return true
	} else {
		return false
	}
}

func (node *DataPathNode) GetUpLinkPDR() (pdr *PDR) {
	return node.UpLinkTunnel.PDR
}

func (node *DataPathNode) GetUpLinkFAR() (far *FAR) {
	return node.UpLinkTunnel.PDR.FAR
}

func (dataPathPool DataPathPool) GetDefaultPath() *DataPath {
	for _, path := range dataPathPool {
		if path.IsDefaultPath {
			return path
		}
	}
	return nil
}

func (dataPath *DataPath) String() string {
	firstDPNode := dataPath.FirstDPNode

	var str string

	str += "DataPath Meta Information\n"
	str += "Activated: " + strconv.FormatBool(dataPath.Activated) + "\n"
	str += "IsDefault Path: " + strconv.FormatBool(dataPath.IsDefaultPath) + "\n"
	str += "Has Braching Point: " + strconv.FormatBool(dataPath.HasBranchingPoint) + "\n"
	str += "Destination IP: " + dataPath.Destination.DestinationIP + "\n"
	str += "Destination Port: " + dataPath.Destination.DestinationPort + "\n"

	str += "DataPath Routing Information\n"
	index := 1
	for curDPNode := firstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		str += strconv.Itoa(index) + "th Node in the Path\n"
		str += "Current UPF IP: " + curDPNode.GetNodeIP() + "\n"
		if curDPNode.Prev() != nil {
			str += "Previous UPF IP: " + curDPNode.Prev().GetNodeIP() + "\n"
		} else {
			str += "Previous UPF IP: None\n"
		}

		if curDPNode.Next() != nil {
			str += "Next UPF IP: " + curDPNode.Next().GetNodeIP() + "\n"
		} else {
			str += "Next UPF IP: None\n"
		}

		index++
	}

	return str
}

func getUrrIdKey(uuid string, urrId uint32) string {
	return uuid + ":" + strconv.Itoa(int(urrId))
}

func (node DataPathNode) addUrrToNode(smContext *SMContext, urrId uint32, isMeasurePkt, isMeasureBeforeQos bool) {
	var urr *URR
	var ok bool
	var err error
	currentUUID := node.UPF.UUID()
	id := getUrrIdKey(currentUUID, urrId)

	if urr, ok = smContext.UrrUpfMap[id]; !ok {
		if urr, err = node.UPF.AddURR(urrId,
			NewMeasureInformation(isMeasurePkt, isMeasureBeforeQos),
			NewMeasurementPeriod(smContext.UrrReportTime),
			NewVolumeThreshold(smContext.UrrReportThreshold)); err != nil {
			logger.PduSessLog.Errorln("new URR failed")
			return
		}
		smContext.UrrUpfMap[id] = urr
	}

	if urr != nil {
		if node.UpLinkTunnel != nil && node.UpLinkTunnel.PDR != nil {
			node.UpLinkTunnel.PDR.URR = append(node.UpLinkTunnel.PDR.URR, urr)
		}
		if node.DownLinkTunnel != nil && node.DownLinkTunnel.PDR != nil {
			node.DownLinkTunnel.PDR.URR = append(node.DownLinkTunnel.PDR.URR, urr)
		}
	}
}

func (datapath *DataPath) addUrrToPath(smContext *SMContext) {
	if smContext.UrrReportTime == 0 && smContext.UrrReportThreshold == 0 {
		logger.PduSessLog.Errorln("URR Report time and threshold is 0")
		return
	}

	for curDataPathNode := datapath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		var MBQEUrrId uint32
		var MAQEUrrId uint32

		if curDataPathNode.IsANUPF() {
			if curDataPathNode.Next() == nil {
				MBQEUrrId = smContext.UrrIdMap[N3N6_MBEQ_URR]
				MAQEUrrId = smContext.UrrIdMap[N3N6_MAEQ_URR]
			} else {
				MBQEUrrId = smContext.UrrIdMap[N3N9_MBEQ_URR]
				MAQEUrrId = smContext.UrrIdMap[N3N9_MAEQ_URR]
			}
		} else {
			MBQEUrrId = smContext.UrrIdMap[N9N6_MBEQ_URR]
			MAQEUrrId = smContext.UrrIdMap[N9N6_MAEQ_URR]
		}
		curDataPathNode.addUrrToNode(smContext, MBQEUrrId, true, true)
		curDataPathNode.addUrrToNode(smContext, MAQEUrrId, true, false)
	}
}

func (dataPath *DataPath) ActivateTunnelAndPDR(smContext *SMContext, precedence uint32) {
	smContext.AllocateLocalSEIDForDataPath(dataPath)

	firstDPNode := dataPath.FirstDPNode
	logger.PduSessLog.Traceln("In ActivateTunnelAndPDR")
	logger.PduSessLog.Traceln(dataPath.String())
	// Activate Tunnels
	for node := firstDPNode; node != nil; node = node.Next() {
		logger.PduSessLog.Traceln("Current DP Node IP: ", node.UPF.NodeID.ResolveNodeIdToIp().String())
		if err := node.ActivateUpLinkTunnel(smContext); err != nil {
			logger.CtxLog.Warnln(err)
			return
		}
		if err := node.ActivateDownLinkTunnel(smContext); err != nil {
			logger.CtxLog.Warnln(err)
			return
		}
	}

	// Note: This should be after Activate Tunnels
	if smContext.UrrReportTime != 0 || smContext.UrrReportThreshold != 0 {
		dataPath.addUrrToPath(smContext)
		logger.PduSessLog.Warn("Create URR")
	} else {
		logger.PduSessLog.Warn("No Create URR")
	}

	sessionRule := smContext.SelectedSessionRule()

	// Activate PDR
	for curDataPathNode := firstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		var defaultQER *QER
		var ambrQER *QER
		currentUUID := curDataPathNode.UPF.uuid
		if qerId, ok := smContext.AMBRQerMap[currentUUID]; !ok {
			if newQER, err := curDataPathNode.UPF.AddQER(); err != nil {
				logger.PduSessLog.Errorln("new QER failed")
				return
			} else {
				newQER.GateStatus = &pfcpType.GateStatus{
					ULGate: pfcpType.GateOpen,
					DLGate: pfcpType.GateOpen,
				}
				newQER.MBR = &pfcpType.MBR{
					ULMBR: util.BitRateTokbps(sessionRule.AuthSessAmbr.Uplink),
					DLMBR: util.BitRateTokbps(sessionRule.AuthSessAmbr.Downlink),
				}
				ambrQER = newQER
			}
			smContext.AMBRQerMap[currentUUID] = ambrQER.QERID
		} else if oldQER, ok := curDataPathNode.UPF.qerPool.Load(qerId); ok {
			ambrQER = oldQER.(*QER)
		}

		if dataPath.IsDefaultPath {
			id := getQosIdKey(currentUUID, sessionRule.DefQosQFI)
			if qerId, ok := smContext.QerUpfMap[id]; !ok {
				if newQER, err := curDataPathNode.UPF.AddQER(); err != nil {
					logger.PduSessLog.Errorln("new QER failed")
					return
				} else {
					newQER.QFI.QFI = sessionRule.DefQosQFI
					newQER.GateStatus = &pfcpType.GateStatus{
						ULGate: pfcpType.GateOpen,
						DLGate: pfcpType.GateOpen,
					}
					defaultQER = newQER
				}
				smContext.QerUpfMap[id] = defaultQER.QERID
			} else if oldQER, ok := curDataPathNode.UPF.qerPool.Load(qerId); ok {
				defaultQER = oldQER.(*QER)
			}
		}

		logger.CtxLog.Traceln("Calculate ", curDataPathNode.UPF.PFCPAddr().String())
		curULTunnel := curDataPathNode.UpLinkTunnel
		curDLTunnel := curDataPathNode.DownLinkTunnel

		// Setup UpLink PDR
		if curULTunnel != nil {
			ULPDR := curULTunnel.PDR
			ULDestUPF := curULTunnel.DestEndPoint.UPF
			if defaultQER != nil {
				ULPDR.QER = append(ULPDR.QER, defaultQER)
			}
			if ambrQER != nil && !dataPath.GBRFlow {
				ULPDR.QER = append(ULPDR.QER, ambrQER)
			}

			ULPDR.Precedence = precedence

			var iface *UPFInterfaceInfo
			if curDataPathNode.IsANUPF() {
				iface = ULDestUPF.GetInterface(models.UpInterfaceType_N3, smContext.Dnn)
			} else {
				iface = ULDestUPF.GetInterface(models.UpInterfaceType_N9, smContext.Dnn)
			}

			if iface == nil {
				logger.CtxLog.Errorln("Can not get interface")
				return
			}

			if upIP, err := iface.IP(smContext.SelectedPDUSessionType); err != nil {
				logger.CtxLog.Errorln("ActivateTunnelAndPDR failed", err)
				return
			} else {
				ULPDR.PDI = PDI{
					SourceInterface: pfcpType.SourceInterface{InterfaceValue: pfcpType.SourceInterfaceAccess},
					LocalFTeid: &pfcpType.FTEID{
						V4:          true,
						Ipv4Address: upIP,
						Teid:        curULTunnel.TEID,
					},
					NetworkInstance: &pfcpType.NetworkInstance{
						NetworkInstance: smContext.Dnn,
						FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
					},
					UEIPAddress: &pfcpType.UEIPAddress{
						V4:          true,
						Ipv4Address: smContext.PDUAddress.To4(),
					},
				}
			}

			ULPDR.OuterHeaderRemoval = &pfcpType.OuterHeaderRemoval{
				OuterHeaderRemovalDescription: pfcpType.OuterHeaderRemovalGtpUUdpIpv4,
			}

			ULFAR := ULPDR.FAR
			ULFAR.ApplyAction = pfcpType.ApplyAction{
				Buff: false,
				Drop: false,
				Dupl: false,
				Forw: true,
				Nocp: false,
			}
			ULFAR.ForwardingParameters = &ForwardingParameters{
				DestinationInterface: pfcpType.DestinationInterface{
					InterfaceValue: pfcpType.DestinationInterfaceCore,
				},
				NetworkInstance: &pfcpType.NetworkInstance{
					NetworkInstance: smContext.Dnn,
					FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
				},
			}

			if curDataPathNode.IsAnchorUPF() {
				ULFAR.ForwardingParameters.
					DestinationInterface.InterfaceValue = pfcpType.DestinationInterfaceSgiLanN6Lan
			}

			if nextULDest := curDataPathNode.Next(); nextULDest != nil {
				nextULTunnel := nextULDest.UpLinkTunnel
				iface = nextULTunnel.DestEndPoint.UPF.GetInterface(models.UpInterfaceType_N9, smContext.Dnn)

				if upIP, err := iface.IP(smContext.SelectedPDUSessionType); err != nil {
					logger.CtxLog.Errorln("ActivateTunnelAndPDR failed", err)
					return
				} else {
					ULFAR.ForwardingParameters.OuterHeaderCreation = &pfcpType.OuterHeaderCreation{
						OuterHeaderCreationDescription: pfcpType.OuterHeaderCreationGtpUUdpIpv4,
						Ipv4Address:                    upIP,
						Teid:                           nextULTunnel.TEID,
					}
				}
			}
		}

		// Setup DownLink
		if curDLTunnel != nil {
			var iface *UPFInterfaceInfo
			DLPDR := curDLTunnel.PDR
			DLDestUPF := curDLTunnel.DestEndPoint.UPF
			if defaultQER != nil {
				DLPDR.QER = append(DLPDR.QER, defaultQER)
			}
			if ambrQER != nil && !dataPath.GBRFlow {
				DLPDR.QER = append(DLPDR.QER, ambrQER)
			}

			DLPDR.Precedence = precedence

			// TODO: Should delete this after FR5GC-1029 is solved
			// TODO: Should delete this after FR5GC-1029 is solved
			if curDataPathNode.IsAnchorUPF() {
				DLPDR.PDI = PDI{
					SourceInterface: pfcpType.SourceInterface{
						InterfaceValue: pfcpType.SourceInterfaceSgiLanN6Lan,
					},
					NetworkInstance: &pfcpType.NetworkInstance{
						NetworkInstance: smContext.Dnn,
						FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
					},
					UEIPAddress: &pfcpType.UEIPAddress{
						V4:          true,
						Sd:          true,
						Ipv4Address: smContext.PDUAddress.To4(),
					},
				}
			} else {
				DLPDR.OuterHeaderRemoval = &pfcpType.OuterHeaderRemoval{
					OuterHeaderRemovalDescription: pfcpType.OuterHeaderRemovalGtpUUdpIpv4,
				}

				iface = DLDestUPF.GetInterface(models.UpInterfaceType_N9, smContext.Dnn)
				if upIP, err := iface.IP(smContext.SelectedPDUSessionType); err != nil {
					logger.CtxLog.Errorln("ActivateTunnelAndPDR failed", err)
					return
				} else {
					DLPDR.PDI = PDI{
						SourceInterface: pfcpType.SourceInterface{InterfaceValue: pfcpType.SourceInterfaceCore},
						LocalFTeid: &pfcpType.FTEID{
							V4:          true,
							Ipv4Address: upIP,
							Teid:        curDLTunnel.TEID,
						},

						// TODO: Should Uncomment this after FR5GC-1029 is solved
						// UEIPAddress: &pfcpType.UEIPAddress{
						// 	V4:          true,
						// 	Ipv4Address: smContext.PDUAddress.To4(),
						// },
					}
				}
			}

			DLFAR := DLPDR.FAR

			logger.PduSessLog.Traceln("Current DP Node IP: ", curDataPathNode.UPF.NodeID.ResolveNodeIdToIp().String())
			logger.PduSessLog.Traceln("Before DLPDR OuterHeaderCreation")
			if nextDLDest := curDataPathNode.Prev(); nextDLDest != nil {
				logger.PduSessLog.Traceln("In DLPDR OuterHeaderCreation")
				nextDLTunnel := nextDLDest.DownLinkTunnel

				DLFAR.ApplyAction = pfcpType.ApplyAction{
					Buff: false,
					Drop: false,
					Dupl: false,
					Forw: true,
					Nocp: false,
				}

				iface = nextDLDest.UPF.GetInterface(models.UpInterfaceType_N9, smContext.Dnn)

				if upIP, err := iface.IP(smContext.SelectedPDUSessionType); err != nil {
					logger.CtxLog.Errorln("ActivateTunnelAndPDR failed", err)
					return
				} else {
					DLFAR.ForwardingParameters = &ForwardingParameters{
						DestinationInterface: pfcpType.DestinationInterface{InterfaceValue: pfcpType.DestinationInterfaceAccess},
						OuterHeaderCreation: &pfcpType.OuterHeaderCreation{
							OuterHeaderCreationDescription: pfcpType.OuterHeaderCreationGtpUUdpIpv4,
							Ipv4Address:                    upIP,
							Teid:                           nextDLTunnel.TEID,
						},
					}
				}
			} else {
				ANUPF := dataPath.FirstDPNode
				DLPDR := ANUPF.DownLinkTunnel.PDR
				DLFAR := DLPDR.FAR
				DLFAR.ForwardingParameters = new(ForwardingParameters)
				DLFAR.ForwardingParameters.DestinationInterface.InterfaceValue = pfcpType.DestinationInterfaceAccess

				if anIP := smContext.Tunnel.ANInformation.IPAddress; anIP != nil {
					DLFAR.ForwardingParameters.NetworkInstance = &pfcpType.NetworkInstance{
						NetworkInstance: smContext.Dnn,
						FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
					}
					DLFAR.ForwardingParameters.OuterHeaderCreation = new(pfcpType.OuterHeaderCreation)

					dlOuterHeaderCreation := DLFAR.ForwardingParameters.OuterHeaderCreation
					dlOuterHeaderCreation.OuterHeaderCreationDescription = pfcpType.OuterHeaderCreationGtpUUdpIpv4
					dlOuterHeaderCreation.Teid = smContext.Tunnel.ANInformation.TEID
					dlOuterHeaderCreation.Ipv4Address = smContext.Tunnel.ANInformation.IPAddress.To4()
				}
			}
		}
		if curDataPathNode.DownLinkTunnel != nil {
			if curDataPathNode.DownLinkTunnel.SrcEndPoint == nil {
				DNDLPDR := curDataPathNode.DownLinkTunnel.PDR
				DNDLPDR.PDI = PDI{
					SourceInterface: pfcpType.SourceInterface{InterfaceValue: pfcpType.SourceInterfaceCore},
					NetworkInstance: &pfcpType.NetworkInstance{
						NetworkInstance: smContext.Dnn,
						FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
					},
					UEIPAddress: &pfcpType.UEIPAddress{
						V4:          true,
						Ipv4Address: smContext.PDUAddress.To4(),
					},
				}
			}
		}
	}

	dataPath.Activated = true
}

func (dataPath *DataPath) DeactivateTunnelAndPDR(smContext *SMContext) {
	firstDPNode := dataPath.FirstDPNode

	var targetNodes []*DataPathNode
	for node := firstDPNode; node != nil; node = node.Next() {
		targetNodes = append(targetNodes, node)
	}
	// Deactivate Tunnels
	for _, node := range targetNodes {
		node.DeactivateUpLinkTunnel(smContext)
		node.DeactivateDownLinkTunnel(smContext)
	}

	dataPath.Activated = false
}

func (p *DataPath) RemovePDR() {
	for curDPNode := p.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if curDPNode.DownLinkTunnel != nil && curDPNode.DownLinkTunnel.PDR != nil {
			curDPNode.DownLinkTunnel.PDR.State = RULE_REMOVE
			curDPNode.DownLinkTunnel.PDR.FAR.State = RULE_REMOVE
		}
		if curDPNode.UpLinkTunnel != nil && curDPNode.UpLinkTunnel.PDR != nil {
			curDPNode.UpLinkTunnel.PDR.State = RULE_REMOVE
			curDPNode.UpLinkTunnel.PDR.FAR.State = RULE_REMOVE
		}
	}
}

func (p *DataPath) AddQoS(smContext *SMContext, qfi uint8, qos *models.QosData) {
	if qos == nil {
		return
	}
	for node := p.FirstDPNode; node != nil; node = node.Next() {
		var qer *QER

		currentUUID := node.UPF.GetUUID()
		qfi := uint8(qos.Var5qi)
		id := getQosIdKey(currentUUID, qfi)

		if qerId, ok := smContext.QerUpfMap[id]; !ok {
			if newQER, err := node.UPF.AddQER(); err != nil {
				logger.PduSessLog.Errorln("new QER failed")
				return
			} else {
				newQER.QFI = pfcpType.QFI{
					QFI: qfi,
				}
				newQER.GateStatus = &pfcpType.GateStatus{
					ULGate: pfcpType.GateOpen,
					DLGate: pfcpType.GateOpen,
				}
				newQER.MBR = &pfcpType.MBR{
					ULMBR: util.BitRateTokbps(qos.MaxbrUl),
					DLMBR: util.BitRateTokbps(qos.MaxbrDl),
				}
				if isGBRFlow(qos) {
					newQER.GBR = &pfcpType.GBR{
						ULGBR: util.BitRateTokbps(qos.GbrUl),
						DLGBR: util.BitRateTokbps(qos.GbrDl),
					}
				}
				qer = newQER
			}
			smContext.QerUpfMap[id] = qer.QERID
		} else if oldQER := node.UPF.GetQERById(qerId); ok {
			if oldQER != nil {
				qer = oldQER
			}
		}
		if qer != nil {
			if node.UpLinkTunnel != nil && node.UpLinkTunnel.PDR != nil {
				node.UpLinkTunnel.PDR.QER = append(node.UpLinkTunnel.PDR.QER, qer)
			}
			if node.DownLinkTunnel != nil && node.DownLinkTunnel.PDR != nil {
				node.DownLinkTunnel.PDR.QER = append(node.DownLinkTunnel.PDR.QER, qer)
			}
		}
	}
}

func (p *DataPath) UpdateFlowDescription(ulFlowDesc, dlFlowDesc string) {
	for curDPNode := p.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		curDPNode.DownLinkTunnel.PDR.PDI.SDFFilter = &pfcpType.SDFFilter{
			Bid:                     false,
			Fl:                      false,
			Spi:                     false,
			Ttc:                     false,
			Fd:                      true,
			LengthOfFlowDescription: uint16(len(dlFlowDesc)),
			FlowDescription:         []byte(dlFlowDesc),
		}
		curDPNode.UpLinkTunnel.PDR.PDI.SDFFilter = &pfcpType.SDFFilter{
			Bid:                     false,
			Fl:                      false,
			Spi:                     false,
			Ttc:                     false,
			Fd:                      true,
			LengthOfFlowDescription: uint16(len(ulFlowDesc)),
			FlowDescription:         []byte(ulFlowDesc),
		}
	}
}

func (p *DataPath) AddForwardingParameters(fwdPolicyID string, teid uint32) {
	for curDPNode := p.FirstDPNode; curDPNode != nil; curDPNode = curDPNode.Next() {
		if curDPNode.IsAnchorUPF() {
			curDPNode.UpLinkTunnel.PDR.FAR.ForwardingParameters.ForwardingPolicyID = fwdPolicyID
			// TODO: Support the RouteInfo in targetTraRouting
			// TODO: Check the message is only presents one of RouteInfo or RouteProfId and sends failure message to the PCF
			// } else if routeInfo := targetTraRouting.RouteInfo; routeInfo != nil {
			// 	locToRouteIP := net.ParseIP(routeInfo.Ipv4Addr)
			// 	curDPNode.UpLinkTunnel.PDR.FAR.ForwardingParameters.OuterHeaderCreation = &pfcpType.OuterHeaderCreation{
			// 		OuterHeaderCreationDescription: pfcpType.OuterHeaderCreationUdpIpv4,
			// 		Ipv4Address:                    locToRouteIP,
			// 		PortNumber:                     uint16(routeInfo.PortNumber),
			// 	}
			// }
		}
		// get old TEID
		// TODO: remove this if RAN tunnel issue is fixed, because the AN tunnel is only one
		if curDPNode.IsANUPF() {
			curDPNode.UpLinkTunnel.PDR.PDI.LocalFTeid.Teid = teid
		}
	}
}

func (dataPath *DataPath) CopyFirstDPNode() *DataPathNode {
	if dataPath.FirstDPNode == nil {
		return nil
	}
	var firstNode *DataPathNode = nil
	var parentNode *DataPathNode = nil
	for node := dataPath.FirstDPNode; node != nil; node = node.Next() {
		newNode := NewDataPathNode()
		if firstNode == nil {
			firstNode = newNode
		}
		newNode.UPF = node.UPF
		if parentNode != nil {
			newNode.AddPrev(parentNode)
			parentNode.AddNext(newNode)
		}
		parentNode = newNode
	}
	return firstNode
}

func getQosIdKey(uuid uuid.UUID, qfi uint8) string {
	return uuid.String() + ":" + strconv.Itoa(int(qfi))
}

func isGBRFlow(qos *models.QosData) bool {
	if qos == nil {
		return false
	}
	_, ok := standardGbr5QIs[qos.Var5qi]
	return ok
}
