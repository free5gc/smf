package service

import (
	"context"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/internal/sbi"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/association"
	"github.com/free5gc/smf/pkg/factory"
)

var _ App = &SmfApp{}

type App interface {
	Config() *factory.Config
	Context() *smf_context.SMFContext
}

var SMF App

type SmfApp struct {
	App

	cfg    *factory.Config
	smfCtx *smf_context.SMFContext
	ctx    context.Context

	sbiServer *sbi.Server
	wg        sync.WaitGroup
}

func NewApp(cfg *factory.Config, tlsKeyLogPath string) (*SmfApp, error) {
	smf := &SmfApp{
		cfg: cfg,
		wg:  sync.WaitGroup{},
	}
	smf.SetLogEnable(cfg.GetLogEnable())
	smf.SetLogLevel(cfg.GetLogLevel())
	smf.SetReportCaller(cfg.GetLogReportCaller())

	// TODO: Initialize sbi server
	sbiServer, err := sbi.NewServer(smf, tlsKeyLogPath)
	if err != nil {
		return nil, err
	}
	smf.sbiServer = sbiServer

	smf_context.Init()
	smf.smfCtx = smf_context.GetSelf()

	SMF = smf

	return smf, nil
}

func (a *SmfApp) Config() *factory.Config {
	return a.cfg
}

func (a *SmfApp) Context() *smf_context.SMFContext {
	return a.smfCtx
}

func (a *SmfApp) CancelContext() context.Context {
	return a.ctx
}

func (a *SmfApp) SetLogEnable(enable bool) {
	logger.MainLog.Infof("Log enable is set to [%v]", enable)
	if enable && logger.Log.Out == os.Stderr {
		return
	} else if !enable && logger.Log.Out == io.Discard {
		return
	}

	a.cfg.SetLogEnable(enable)
	if enable {
		logger.Log.SetOutput(os.Stderr)
	} else {
		logger.Log.SetOutput(io.Discard)
	}
}

func (a *SmfApp) SetLogLevel(level string) {
	lvl, err := logrus.ParseLevel(level)
	if err != nil {
		logger.MainLog.Warnf("Log level [%s] is invalid", level)
		return
	}

	logger.MainLog.Infof("Log level is set to [%s]", level)
	if lvl == logger.Log.GetLevel() {
		return
	}

	a.cfg.SetLogLevel(level)
	logger.Log.SetLevel(lvl)
}

func (a *SmfApp) SetReportCaller(reportCaller bool) {
	logger.MainLog.Infof("Report Caller is set to [%v]", reportCaller)
	if reportCaller == logger.Log.ReportCaller {
		return
	}

	a.cfg.SetLogReportCaller(reportCaller)
	logger.Log.SetReportCaller(reportCaller)
}

func (a *SmfApp) Start(tlsKeyLogPath string) {
	logger.InitLog.Infoln("Server started")

	a.sbiServer.Run(context.Background(), &a.wg)
	go a.listenShutDownEvent()

	udp.Run(pfcp.Dispatch)

	ctx, cancel := context.WithCancel(context.Background())
	smf_context.GetSelf().Ctx = ctx
	smf_context.GetSelf().PFCPCancelFunc = cancel
	for _, upNode := range smf_context.GetSelf().UserPlaneInformation.UPFs {
		upNode.UPF.Ctx, upNode.UPF.CancelFunc = context.WithCancel(context.Background())
		go association.ToBeAssociatedWithUPF(ctx, upNode.UPF)
	}

	time.Sleep(1000 * time.Millisecond)
}

func (a *SmfApp) listenShutDownEvent() {
	defer func() {
		if p := recover(); p != nil {
			// Print stack for panic to log. Fatalf() will let program exit.
			logger.MainLog.Fatalf("panic: %v\n%s", p, string(debug.Stack()))
		}
		a.wg.Done()
	}()

	<-a.ctx.Done()
	a.Terminate()
	a.sbiServer.Stop()
	a.wg.Wait()
}

func (a *SmfApp) Terminate() {
	logger.MainLog.Infof("Terminating SMF...")
	// deregister with NRF
	problemDetails, err := a.consumer.SendDeregisterNFInstance()
	if problemDetails != nil {
		logger.MainLog.Errorf("Deregister NF instance Failed Problem[%+v]", problemDetails)
	} else if err != nil {
		logger.MainLog.Errorf("Deregister NF instance Error[%+v]", err)
	} else {
		logger.MainLog.Infof("Deregister from NRF successfully")
	}
	logger.MainLog.Infof("SMF SBI Server terminated")
}
