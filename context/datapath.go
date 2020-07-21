package context

import (
	"fmt"
	"free5gc/lib/pfcp/pfcpType"
	"free5gc/lib/util_3gpp"
	"free5gc/src/smf/logger"
	"strconv"
)

// GTPTunnel represents the GTP tunnel information
type GTPTunnel struct {
	SrcEndPoint  *DataPathNode
	DestEndPoint *DataPathNode

	TEID uint32
	PDR  *PDR
}

type DataPathNode struct {
	UPF *UPF
	//DataPathToAN *DataPathDownLink
	//DataPathToDN map[string]*DataPathUpLink //uuid to DataPathLink

	UpLinkTunnel   *GTPTunnel
	DownLinkTunnel *GTPTunnel
	//for UE Routing Topology
	//for special case:
	//branching & leafnode

	//InUse                bool
	IsBranchingPoint bool
	//DLDataPathLinkForPSA *DataPathUpLink
	//BPUpLinkPDRs         map[string]*DataPathDownLink // uuid to UpLink

	HaveSession bool
}

type DataPath struct {
	//meta data
	Activated         bool
	IsDefaultPath     bool
	Destination       Destination
	HasBranchingPoint bool
	//Data Path Double Link List
	FirstDPNode *DataPathNode
}

type DataPathPool map[int64]*DataPath

type Destination struct {
	DestinationIP   string
	DestinationPort string
	Url             string
}

func NewDataPathNode() (node *DataPathNode) {
	node = &DataPathNode{
		UpLinkTunnel:   &GTPTunnel{},
		DownLinkTunnel: &GTPTunnel{},
	}
	return
}

func NewDataPath() (dataPath *DataPath) {
	dataPath = &DataPath{
		Destination: Destination{
			DestinationIP:   "",
			DestinationPort: "",
			Url:             "",
		},
	}

	return
}

func NewDataPathPool() (pool DataPathPool) {
	pool = make(map[int64]*DataPath)
	return
}

func (node *DataPathNode) AddNext(next *DataPathNode) {
	node.DownLinkTunnel.SrcEndPoint = next

	return
}
func (node *DataPathNode) AddPrev(prev *DataPathNode) {
	node.UpLinkTunnel.SrcEndPoint = prev

	return
}

func (node *DataPathNode) Next() (next *DataPathNode) {

	if node.DownLinkTunnel == nil {
		return nil
	}
	next = node.DownLinkTunnel.SrcEndPoint
	return
}

func (node *DataPathNode) Prev() (prev *DataPathNode) {

	if node.UpLinkTunnel == nil {
		return nil
	}
	prev = node.UpLinkTunnel.SrcEndPoint
	return
}

func (node *DataPathNode) ActivateUpLinkTunnel(smContext *SMContext) (err error) {

	logger.CtxLog.Traceln("In ActivateUpLinkTunnel")
	node.UpLinkTunnel.SrcEndPoint = node.Prev()
	node.UpLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	node.UpLinkTunnel.PDR, err = destUPF.AddPDR()

	if err != nil {
		logger.CtxLog.Warnln("In ActivateUpLinkTunnel UPF IP: ", node.UPF.NodeID.ResolveNodeIdToIp().String())
		logger.CtxLog.Warnln("Allocata PDR Error: ", err)
	}

	smContext.PutPDRtoPFCPSession(destUPF.NodeID, node.UpLinkTunnel.PDR)

	teid, _ := destUPF.GenerateTEID()
	node.UpLinkTunnel.TEID = teid

	return
}

func (node *DataPathNode) ActivateDownLinkTunnel(smContext *SMContext) (err error) {

	node.DownLinkTunnel.SrcEndPoint = node.Next()
	node.DownLinkTunnel.DestEndPoint = node

	destUPF := node.UPF
	node.DownLinkTunnel.PDR, err = destUPF.AddPDR()

	if err != nil {
		logger.CtxLog.Warnln("In ActivateDownLinkTunnel UPF IP: ", node.UPF.NodeID.ResolveNodeIdToIp().String())
		logger.CtxLog.Warnln("Allocata PDR Error: ", err)
	}

	smContext.PutPDRtoPFCPSession(destUPF.NodeID, node.DownLinkTunnel.PDR)

	teid, _ := destUPF.GenerateTEID()
	node.DownLinkTunnel.TEID = teid
	return
}

func (node *DataPathNode) DeactivateUpLinkTunnel(smContext *SMContext) {

	pdr := node.UpLinkTunnel.PDR
	far := node.UpLinkTunnel.PDR.FAR
	bar := node.UpLinkTunnel.PDR.FAR.BAR

	smContext.RemovePDRfromPFCPSession(node.UPF.NodeID, pdr)

	err := node.UPF.RemovePDR(pdr)
	if err != nil {
		logger.CtxLog.Warnln("Deactivaed UpLinkTunnel", err)
	}

	err = node.UPF.RemoveFAR(far)
	if err != nil {
		logger.CtxLog.Warnln("Deactivaed UpLinkTunnel", err)
	}

	if bar != nil {
		err = node.UPF.RemoveBAR(bar)
		if err != nil {
			logger.CtxLog.Warnln("Deactivaed UpLinkTunnel", err)
		}
	}

	node.UpLinkTunnel = &GTPTunnel{}
	return
}

func (node *DataPathNode) DeactivateDownLinkTunnel(smContext *SMContext) {

	pdr := node.DownLinkTunnel.PDR
	far := node.DownLinkTunnel.PDR.FAR
	bar := node.DownLinkTunnel.PDR.FAR.BAR

	smContext.RemovePDRfromPFCPSession(node.UPF.NodeID, pdr)

	err := node.UPF.RemovePDR(pdr)
	if err != nil {
		logger.CtxLog.Warnln("Deactivaed DownLinkTunnel", err)
	}

	err = node.UPF.RemoveFAR(far)
	if err != nil {
		logger.CtxLog.Warnln("Deactivaed DownLinkTunnel", err)
	}

	if bar != nil {
		err = node.UPF.RemoveBAR(bar)
		if err != nil {
			logger.CtxLog.Warnln("Deactivaed DownLinkTunnel", err)
		}
	}

	node.DownLinkTunnel = &GTPTunnel{}
	return
}

func (node *DataPathNode) GetUPFID() (id string, err error) {
	node_ip := node.GetNodeIP()
	var exist bool

	if id, exist = smfContext.UserPlaneInformation.UPFsIPtoID[node_ip]; !exist {
		err = fmt.Errorf("UPNode IP %s doesn't exist in smfcfg.conf, please sync the config files!", node_ip)
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

func (dataPathPool DataPathPool) GetDefaultPath() (dataPath *DataPath) {

	for _, path := range dataPathPool {

		if path.IsDefaultPath {
			dataPath = path
			return
		}

	}
	return
}

func (dataPath *DataPath) ToString() (str string) {

	firstDPNode := dataPath.FirstDPNode

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

	return
}

func (dataPath *DataPath) ActivateTunnelAndPDR(smContext *SMContext) {

	firstDPNode := dataPath.FirstDPNode
	logger.PduSessLog.Traceln("In ActivateTunnelAndPDR")
	logger.PduSessLog.Traceln(dataPath.ToString())
	//Activate Tunnels
	for curDataPathNode := firstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		logger.PduSessLog.Traceln("Current DP Node IP: ", curDataPathNode.UPF.NodeID.ResolveNodeIdToIp().String())
		err := curDataPathNode.ActivateUpLinkTunnel(smContext)

		if err != nil {
			logger.CtxLog.Warnln(err)
		}
		err = curDataPathNode.ActivateDownLinkTunnel(smContext)

		if err != nil {
			logger.CtxLog.Warnln(err)
		}
	}

	//Activate PDR
	for curDataPathNode := firstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		logger.CtxLog.Traceln("Calculate ", curDataPathNode.UPF.PFCPAddr().String())
		curULTunnel := curDataPathNode.UpLinkTunnel
		curDLTunnel := curDataPathNode.DownLinkTunnel

		// Setup UpLink PDR
		if curULTunnel != nil {
			ULPDR := curULTunnel.PDR
			ULDestUPF := curULTunnel.DestEndPoint.UPF

			ULPDR.Precedence = 32
			ULPDR.PDI = PDI{
				SourceInterface: pfcpType.SourceInterface{InterfaceValue: pfcpType.SourceInterfaceAccess},
				LocalFTeid: &pfcpType.FTEID{
					V4:          true,
					Ipv4Address: ULDestUPF.UPIPInfo.Ipv4Address,
					Teid:        curULTunnel.TEID,
				},
				UEIPAddress: &pfcpType.UEIPAddress{
					V4:          true,
					Ipv4Address: smContext.PDUAddress.To4(),
				},
			}
			ULPDR.OuterHeaderRemoval = &pfcpType.OuterHeaderRemoval{OuterHeaderRemovalDescription: pfcpType.OuterHeaderRemovalGtpUUdpIpv4}

			ULFAR := ULPDR.FAR
			ULFAR.ApplyAction = pfcpType.ApplyAction{
				Buff: false,
				Drop: false,
				Dupl: false,
				Forw: true,
				Nocp: false,
			}
			ULPDR.FAR.ForwardingParameters = &ForwardingParameters{
				DestinationInterface: pfcpType.DestinationInterface{
					InterfaceValue: pfcpType.DestinationInterfaceAccess,
				},
				NetworkInstance: []byte(smContext.Dnn),
			}

			if nextULDest := curDataPathNode.Next(); nextULDest != nil {
				nextULTunnel := nextULDest.UpLinkTunnel
				ULPDR.FAR.ForwardingParameters.OuterHeaderCreation = &pfcpType.OuterHeaderCreation{
					OuterHeaderCreationDescription: pfcpType.OuterHeaderCreationGtpUUdpIpv4,
					Ipv4Address:                    nextULTunnel.DestEndPoint.UPF.UPIPInfo.Ipv4Address,
					Teid:                           nextULTunnel.TEID,
				}
			}

		}

		// Setup DownLink
		if curDLTunnel != nil {
			DLPDR := curDLTunnel.PDR
			DLDestUPF := curDLTunnel.DestEndPoint.UPF

			DLPDR.Precedence = 32
			DLPDR.PDI = PDI{
				SourceInterface: pfcpType.SourceInterface{InterfaceValue: pfcpType.SourceInterfaceCore},
				LocalFTeid: &pfcpType.FTEID{
					V4:          true,
					Ipv4Address: DLDestUPF.UPIPInfo.Ipv4Address,
					Teid:        curDLTunnel.TEID,
				},

				// TODO: Should Uncomment this after FR5GC-1029 is solved
				// UEIPAddress: &pfcpType.UEIPAddress{
				// 	V4:          true,
				// 	Ipv4Address: smContext.PDUAddress.To4(),
				// },
			}

			// TODO: Should delete this after FR5GC-1029 is solved
			if curDataPathNode.IsAnchorUPF() {
				DLPDR.PDI.UEIPAddress = &pfcpType.UEIPAddress{
					V4:          true,
					Ipv4Address: smContext.PDUAddress.To4(),
				}
			}

			if !curDataPathNode.IsAnchorUPF() {
				DLPDR.OuterHeaderRemoval = &pfcpType.OuterHeaderRemoval{
					OuterHeaderRemovalDescription: pfcpType.OuterHeaderRemovalGtpUUdpIpv4,
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
				DLFAR.ForwardingParameters = &ForwardingParameters{
					DestinationInterface: pfcpType.DestinationInterface{InterfaceValue: pfcpType.DestinationInterfaceAccess},
					OuterHeaderCreation: &pfcpType.OuterHeaderCreation{
						OuterHeaderCreationDescription: pfcpType.OuterHeaderCreationGtpUUdpIpv4,
						Ipv4Address:                    nextDLDest.UPF.UPIPInfo.Ipv4Address,
						Teid:                           nextDLTunnel.TEID,
					},
				}
			}
		}
		if curDataPathNode.DownLinkTunnel != nil {
			if curDataPathNode.DownLinkTunnel.SrcEndPoint == nil {
				DNDLPDR := curDataPathNode.DownLinkTunnel.PDR
				DNDLPDR.PDI = PDI{
					SourceInterface: pfcpType.SourceInterface{InterfaceValue: pfcpType.SourceInterfaceCore},
					NetworkInstance: util_3gpp.Dnn(smContext.Dnn),
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

	//Deactivate Tunnels
	for curDataPathNode := firstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		curDataPathNode.DeactivateUpLinkTunnel(smContext)
		curDataPathNode.DeactivateDownLinkTunnel(smContext)
	}

	dataPath.Activated = false
}
