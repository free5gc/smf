package utils

import (
	"context"
	"fmt"
	"sync"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/pkg/service"
)

func InitPFCPFunc(pCtx context.Context) (func(app *service.SmfApp) error, func()) {
	smfContext := smf_context.GetSelf()
	var server *pfcp.PfcpServer
	var serverWG sync.WaitGroup
	var lifecycleMu sync.Mutex

	pfcpStart := func(app *service.SmfApp) error {
		lifecycleMu.Lock()
		defer lifecycleMu.Unlock()
		if server != nil {
			return fmt.Errorf("PFCP server is already started")
		}

		// PfcpServer is now the sole production owner of UDP 8805 and every
		// Tx/Rx transaction. The shared context controls per-UPF active loops.
		smfContext.PfcpContext, smfContext.PfcpCancelFunc = context.WithCancel(pCtx)
		newServer := pfcp.NewPfcpServer(app, smfContext.ListenIP().String())
		newServer.SetAssociationStateManager(app.Processor())
		newServer.SetSessionReportHandler(app.Processor())
		if err := newServer.Run(&serverWG); err != nil {
			smfContext.PfcpCancelFunc()
			return err
		}

		server = newServer
		app.Processor().SetActivePFCPClient(newServer)

		for _, upNode := range smfContext.UserPlaneInformation.UPFs {
			go app.Processor().ToBeAssociatedWithUPF(smfContext.PfcpContext, upNode.UPF)
		}
		return nil
	}

	pfcpStop := func() {
		lifecycleMu.Lock()
		defer lifecycleMu.Unlock()
		if smfContext.PfcpCancelFunc != nil {
			smfContext.PfcpCancelFunc()
		}
		if server == nil {
			return
		}
		server.Stop()
		serverWG.Wait()
		server = nil
	}

	return pfcpStart, pfcpStop
}
