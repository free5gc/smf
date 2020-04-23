package pfcp_udp

import (
	"net"
	"time"

	"gofree5gc/lib/pfcp"
	"gofree5gc/lib/pfcp/pfcpUdp"
	"gofree5gc/src/smf/context"
	"gofree5gc/src/smf/handler/message"
	"gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/pfcp/pfcp_util"
)

const MaxPfcpUdpDataSize = 1024

var Server pfcpUdp.PfcpServer

var ServerStartTime time.Time

var SeqNumTable *pfcp_util.SeqNumTable

func init() {
	SeqNumTable = pfcp_util.NewSeqNumTable()
}

func Run() {
	CPNodeID := context.SMF_Self().CPNodeID
	if len(CPNodeID.NodeIdValue) != 0 {
		Server.Addr = CPNodeID.ResolveNodeIdToIp().String()
	}
	err := Server.Listen()
	if err != nil {
		logger.PfcpLog.Errorf("Failed to listen: %v", err)
	}
	logger.PfcpLog.Infof("Listen on %s", Server.Conn.LocalAddr().String())

	go func(p *pfcpUdp.PfcpServer) {
		for {
			var pfcpMessage pfcp.Message
			remoteAddr, err := p.ReadFrom(&pfcpMessage)
			if err != nil {
				logger.PfcpLog.Errorf("Read PFCP error: %v", err)
				continue
			}

			seq_num_check_pass := SeqNumTable.RecvCheckAndPutItem(&pfcpMessage)
			if !seq_num_check_pass {
				logger.PfcpLog.Errorf("\nSequence Number checking error.\n")
				continue
			}

			pfcpUdpMessage := pfcpUdp.NewMessage(remoteAddr, &pfcpMessage)

			msg := message.NewPfcpMessage(&pfcpUdpMessage)
			message.SendMessage(msg)
		}
	}(&Server)

	ServerStartTime = time.Now()
}

func SendPfcp(msg pfcp.Message, addr *net.UDPAddr) {
	seq_num_check_pass := SeqNumTable.SendCheckAndPutItem(&msg)
	if !seq_num_check_pass {
		logger.PfcpLog.Errorf("\nSequence Number checking error.\n")
		return
	}

	err := Server.WriteTo(msg, addr)
	if err != nil {
		logger.PfcpLog.Errorf("Failed to send PFCP message: %v", err)
	}

}
