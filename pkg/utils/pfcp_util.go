package utils

import (
	"context"
	"time"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/pkg/service"
)

var (
	pfcpStart func(a *service.SmfApp)
	pfcpStop  func()
)

func InitPFCPFunc() (func(a *service.SmfApp), func()) {
	pfcpStart = func(a *service.SmfApp) {
		// Initialize PFCP server
		ctx, cancel := context.WithCancel(context.Background())
		smf_context.GetSelf().Ctx = ctx
		smf_context.GetSelf().PFCPCancelFunc = cancel

		udp.Run(pfcp.Dispatch)

		// Wait for PFCP start
		time.Sleep(1000 * time.Millisecond)

		for _, upf := range smf_context.GetSelf().UserPlaneInformation.UPFs {
			upf.Ctx, upf.CancelFunc = context.WithCancel(ctx)
			go a.Processor().ToBeAssociatedWithUPF(ctx, upf)
		}
	}

	pfcpStop = func() {
		smf_context.GetSelf().PFCPCancelFunc()
		err := udp.Server.Close()
		if err != nil {
			logger.Log.Errorf("udp server close failed %+v", err)
		}
	}

	return pfcpStart, pfcpStop
}
