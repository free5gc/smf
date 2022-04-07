package udp

import (
	"net"
	"time"

	"github.com/free5gc/pfcp"
	"github.com/free5gc/pfcp/pfcpUdp"
	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

const MaxPfcpUdpDataSize = 1024

var Server *pfcpUdp.PfcpServer

var ServerStartTime time.Time

func Run(Dispatch func(*pfcpUdp.Message)) {
	CPNodeID := context.SMF_Self().CPNodeID
	Server = pfcpUdp.NewPfcpServer(CPNodeID.ResolveNodeIdToIp().String())

	err := Server.Listen()
	if err != nil {
		logger.PfcpLog.Errorf("Failed to listen: %v", err)
	}
	logger.PfcpLog.Infof("Listen on %s", Server.Conn.LocalAddr().String())

	go func(p *pfcpUdp.PfcpServer) {
		for {
			msg, err := p.ReadFrom()
			if err != nil {
				if err == pfcpUdp.ErrReceivedResentRequest {
					logger.PfcpLog.Infoln(err)
				} else {
					logger.PfcpLog.Warnf("Read PFCP error: %v", err)
				}

				continue
			}

			if msg.PfcpMessage.IsRequest() {
				go Dispatch(msg)
			}
		}
	}(Server)

	ServerStartTime = time.Now()
}

func SendPfcpResponse(sndMsg *pfcp.Message, addr *net.UDPAddr) {
	Server.WriteResponseTo(sndMsg, addr)
}

func SendPfcpRequest(sndMsg *pfcp.Message, addr *net.UDPAddr) (rsvMsg *pfcpUdp.Message, err error) {
	return Server.WriteRequestTo(sndMsg, addr)
}
