package udp

import (
	"net"
	"time"

	"free5gc/lib/pfcp"
	"free5gc/lib/pfcp/pfcpUdp"
	"free5gc/src/smf/context"
	"free5gc/src/smf/handler/message"
	"free5gc/src/smf/logger"
	"free5gc/src/smf/pfcp/util"
)

const MaxPfcpUdpDataSize = 1024

var Server pfcpUdp.PfcpServer

var ServerStartTime time.Time

var SeqNumTable *util.SeqNumTable

func init() {
	SeqNumTable = util.NewSeqNumTable()
}

func Run() {
	CPNodeID := context.SMF_Self().CPNodeID
	Server = pfcpUdp.NewPfcpServer(CPNodeID.ResolveNodeIdToIp().String())

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
				if err.Error() == "Receive resend PFCP request" {
					logger.PfcpLog.Infoln(err)
				} else {
					logger.PfcpLog.Warnf("Read PFCP error: %v", err)
				}

				continue
			}

			// seq_num_check_pass := SeqNumTable.RecvCheckAndPutItem(&pfcpMessage)
			// if !seq_num_check_pass {
			// 	logger.PfcpLog.Warnf("\nSequence Number checking error.\n")
			// 	continue
			// }

			pfcpUdpMessage := pfcpUdp.NewMessage(remoteAddr, &pfcpMessage)

			msg := message.NewPfcpMessage(&pfcpUdpMessage)
			message.SendMessage(msg)
		}
	}(&Server)

	ServerStartTime = time.Now()
}

func SendPfcp(msg pfcp.Message, addr *net.UDPAddr) {
	// seq_num_check_pass := SeqNumTable.SendCheckAndPutItem(&msg)
	// if !seq_num_check_pass {
	// 	logger.PfcpLog.Errorf("\nSequence Number checking error.\n")
	// 	return
	// }

	err := Server.WriteTo(msg, addr)
	if err != nil {
		logger.PfcpLog.Errorf("Failed to send PFCP message: %v", err)
	}

}
