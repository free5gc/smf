package context

import (
	"fmt"
	"math"
	"net"

	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/idgenerator"
)

type UEPreConfigPaths struct {
	DataPathPool    DataPathPool
	PathIDGenerator *idgenerator.IDGenerator
}

func NewUEDataPathNode(name string) (node *DataPathNode, err error) {
	upNodes := smfContext.UserPlaneInformation.UPNodes

	if _, exist := upNodes[name]; !exist {
		err = fmt.Errorf("UPNode %s isn't exist in smfcfg.yaml, but in UERouting.yaml", name)
		return nil, err
	}

	node = &DataPathNode{
		UPF:            upNodes[name].UPF,
		UpLinkTunnel:   &GTPTunnel{},
		DownLinkTunnel: &GTPTunnel{},
	}
	return
}

func NewUEPreConfigPaths(paths []factory.SpecificPath) (*UEPreConfigPaths, error) {
	var uePreConfigPaths *UEPreConfigPaths
	ueDataPathPool := NewDataPathPool()
	lowerBound := 0
	pathIDGenerator := idgenerator.NewGenerator(1, math.MaxInt32)

	logger.PduSessLog.Infoln("In NewUEPreConfigPaths")

	for _, path := range paths {
		dataPath := NewDataPath()

		var pathID int64
		if allocPathID, err := pathIDGenerator.Allocate(); err != nil {
			logger.CtxLog.Warnf("Allocate pathID error: %+v", err)
			return nil, err
		} else {
			pathID = allocPathID
		}

		dataPath.Destination.DestinationIP = path.DestinationIP
		dataPath.Destination.DestinationPort = path.DestinationPort
		ueDataPathPool[pathID] = dataPath
		var parentNode *DataPathNode = nil
		for idx, nodeName := range path.Path {
			newUeNode, err := NewUEDataPathNode(nodeName)
			if err != nil {
				return nil, err
			}

			if idx == lowerBound {
				dataPath.FirstDPNode = newUeNode
			}
			if parentNode != nil {
				newUeNode.AddPrev(parentNode)
				parentNode.AddNext(newUeNode)
			}
			parentNode = newUeNode
		}

		logger.CtxLog.Traceln("New data path added")
		logger.CtxLog.Traceln("\n" + dataPath.String() + "\n")
	}

	uePreConfigPaths = &UEPreConfigPaths{
		DataPathPool:    ueDataPathPool,
		PathIDGenerator: pathIDGenerator,
	}
	return uePreConfigPaths, nil
}

func GetUEPreConfigPaths(supi string, upfName string) *UEPreConfigPaths {
	groupName := GetULCLGroupNameFromSUPI(supi)
	if groupName == "" {
		return nil
	}
	dataPathPool := NewDataPathPool()
	dataPathPool[1] = smfContext.UEDefaultPathPool[groupName].GetDefaultPath(upfName)
	var i int64 = 2
	for _, dataPath := range smfContext.UEPreConfigPathPool[groupName].DataPathPool {
		firstNode := dataPath.CopyFirstDPNode()
		path := &DataPath{
			Activated:     false,
			IsDefaultPath: false,
			Destination:   dataPath.Destination,
			FirstDPNode:   firstNode,
		}
		dataPathPool[i] = path
		i++
	}
	paths := &UEPreConfigPaths{
		DataPathPool:    dataPathPool,
		PathIDGenerator: smfContext.UEPreConfigPathPool[groupName].PathIDGenerator,
	}
	return paths
}

func CheckUEHasPreConfig(supi string) (exist bool) {
	groupName := GetULCLGroupNameFromSUPI(supi)
	logger.CtxLog.Tracef("UE [%s] belongs to group [%s]", supi, groupName)
	if groupName == "" {
		return false
	}
	_, exist = smfContext.UEPreConfigPathPool[groupName]
	return
}

func GetULCLGroupNameFromSUPI(supi string) string {
	ulclGroups := smfContext.ULCLGroups
	for name, group := range ulclGroups {
		for _, member := range group {
			if member == supi {
				return name
			}
		}
	}
	return ""
}

func (c *SMContext) findPSAandAllocUeIP(param *UPFSelectionParams) error {
	c.Log.Traceln("findPSAandAllocUeIP")
	if param == nil {
		return fmt.Errorf("UPFSelectionParams is nil")
	}

	upi := GetUserPlaneInformation()
	if GetSelf().ULCLSupport && CheckUEHasPreConfig(c.Supi) {
		groupName := GetULCLGroupNameFromSUPI(c.Supi)
		preConfigPathPool := GetUEDefaultPathPool(groupName)
		if preConfigPathPool != nil {
			selectedUPFName := ""
			selectedUPFName, c.PDUAddress, c.UseStaticIP = preConfigPathPool.SelectUPFAndAllocUEIPForULCL(
				upi, param)
			c.SelectedUPF = upi.UPFs[selectedUPFName]
		}
	} else {
		c.SelectedUPF, c.PDUAddress, c.UseStaticIP = upi.SelectUPFAndAllocUEIP(param)
		c.Log.Infof("Allocated PDUAdress[%s]", c.PDUAddress.String())
	}
	if c.PDUAddress == nil {
		return fmt.Errorf("fail to allocate PDU address, Selection Parameter: %s",
			param.String())
	}
	return nil
}

func (c *SMContext) AllocUeIP() error {
	c.SelectionParam = &UPFSelectionParams{
		Dnn: c.Dnn,
		SNssai: &SNssai{
			Sst: c.SNssai.Sst,
			Sd:  c.SNssai.Sd,
		},
	}

	if len(c.DnnConfiguration.StaticIpAddress) > 0 {
		staticIPConfig := c.DnnConfiguration.StaticIpAddress[0]
		if staticIPConfig.Ipv4Addr != "" {
			c.SelectionParam.PDUAddress = net.ParseIP(staticIPConfig.Ipv4Addr).To4()
		}
	}

	if err := c.findPSAandAllocUeIP(c.SelectionParam); err != nil {
		return err
	}
	return nil
}

// This function create ULCL data paths.
func (c *SMContext) SelectULCLDataPaths() error {
	if c.SelectionParam == nil || c.SelectedUPF == nil {
		return fmt.Errorf("SelectULCLDataPath err: SelectionParam or SelectedUPF is nil")
	}

	if GetSelf().ULCLSupport && CheckUEHasPreConfig(c.Supi) {
		c.Log.Infof("Has pre-config ULCL paths")
		uePreConfigPaths := GetUEPreConfigPaths(c.Supi, c.SelectedUPF.Name)
		for _, dp := range uePreConfigPaths.DataPathPool {
			if !dp.IsDefaultPath {
				c.Tunnel.AddDataPath(dp)
			}
		}
	}
	return nil
}

// This function create a data path to be default data path.
func (c *SMContext) SelectDefaultDataPath() error {
	if c.SelectionParam == nil || c.SelectedUPF == nil {
		return fmt.Errorf("SelectDefaultDataPath err: SelectionParam or SelectedUPF is nil")
	}

	defaultPath := c.Tunnel.DataPathPool.GetDefaultPath()
	if defaultPath != nil {
		// A default path already exists.
		// Use this one.
		c.Log.Infof("Has default path")
		defaultPath = c.Tunnel.DataPathPool.GetDefaultPath()
	} else if GetSelf().ULCLSupport && CheckUEHasPreConfig(c.Supi) {
		// Fallback on pre-config default path
		c.Log.Infof("Has pre-config default path")
		uePreConfigPaths := GetUEPreConfigPaths(c.Supi, c.SelectedUPF.Name)
		defaultPath = uePreConfigPaths.DataPathPool.GetDefaultPath()
		c.Tunnel.AddDataPath(defaultPath)
	} else {
		// UE has no pre-config path and default path
		// Use default route
		c.Log.Infof("Has no pre-config route. Has no default path")
		defaultUPPath := GetUserPlaneInformation().GetDefaultUserPlanePathByDNNAndUPF(
			c.SelectionParam, c.SelectedUPF)
		defaultPath = GenerateDataPath(defaultUPPath)
		if defaultPath != nil {
			defaultPath.IsDefaultPath = true
			c.Tunnel.AddDataPath(defaultPath)
		}
	}

	if defaultPath == nil {
		return fmt.Errorf("data path not found, Selection Parameter: %s",
			c.SelectionParam.String())
	}

	if !defaultPath.Activated {
		binding := PathQoSBinding{QFI: c.SelectedSessionRule().DefQosQFI}
		if err := defaultPath.ActivateTunnelAndPDR(c, DefaultPrecedence, binding); err != nil {
			return err
		}
	}

	return nil
}
