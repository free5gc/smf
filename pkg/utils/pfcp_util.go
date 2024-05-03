package utils

import (
	"context"
	"time"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/pkg/association"
	"github.com/free5gc/smf/pkg/service"
)

var (
	pfcpStart func(a *service.SmfApp)
	pfcpStop  func()
)

func InitPFCPFunc() (func(a *service.SmfApp), func()) {
	pfcpStart = func(a *service.SmfApp) {
		// Initialize PFCP server
		udp.Run(pfcp.Dispatch)

		ctx, cancel := context.WithCancel(context.Background())
		smf_context.GetSelf().Ctx = ctx
		smf_context.GetSelf().PFCPCancelFunc = cancel
		for _, upNode := range smf_context.GetSelf().UserPlaneInformation.UPFs {
			upNode.UPF.Ctx, upNode.UPF.CancelFunc = context.WithCancel(context.Background())
			go association.ToBeAssociatedWithUPF(ctx, upNode.UPF, a.Processor())
		}

		// Wait for PFCF start
		time.Sleep(1000 * time.Millisecond)
	}

	pfcpStop = func() {
		udp.Server.Close()
	}

	return pfcpStart, pfcpStop
}
