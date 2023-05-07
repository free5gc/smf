package service

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/internal/sbi/callback"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/eventexposure"
	"github.com/free5gc/smf/internal/sbi/oam"
	"github.com/free5gc/smf/internal/sbi/pdusession"
	"github.com/free5gc/smf/internal/sbi/upi"
	"github.com/free5gc/smf/pkg/association"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/httpwrapper"
	logger_util "github.com/free5gc/util/logger"
)

type SmfApp struct {
	cfg    *factory.Config
	smfCtx *smf_context.SMFContext
}

func NewApp(cfg *factory.Config) (*SmfApp, error) {
	smf := &SmfApp{cfg: cfg}
	smf.SetLogEnable(cfg.GetLogEnable())
	smf.SetLogLevel(cfg.GetLogLevel())
	smf.SetReportCaller(cfg.GetLogReportCaller())

	smf_context.Init()
	smf.smfCtx = smf_context.GetSelf()
	return smf, nil
}

func (a *SmfApp) SetLogEnable(enable bool) {
	logger.MainLog.Infof("Log enable is set to [%v]", enable)
	if enable && logger.Log.Out == os.Stderr {
		return
	} else if !enable && logger.Log.Out == ioutil.Discard {
		return
	}

	a.cfg.SetLogEnable(enable)
	if enable {
		logger.Log.SetOutput(os.Stderr)
	} else {
		logger.Log.SetOutput(ioutil.Discard)
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
	pemPath := factory.SmfDefaultCertPemPath
	keyPath := factory.SmfDefaultPrivateKeyPath
	sbi := factory.SmfConfig.Configuration.Sbi
	if sbi.Tls != nil {
		pemPath = sbi.Tls.Pem
		keyPath = sbi.Tls.Key
	}

	smf_context.InitSmfContext(factory.SmfConfig)
	// allocate id for each upf
	smf_context.AllocateUPFID()
	smf_context.InitSMFUERouting(factory.UERoutingConfig)

	logger.InitLog.Infoln("Server started")
	router := logger_util.NewGinWithLogrus(logger.GinLog)

	err := consumer.SendNFRegistration()
	if err != nil {
		retry_err := consumer.RetrySendNFRegistration(10)
		if retry_err != nil {
			logger.InitLog.Errorln(retry_err)
			return
		}
	}

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				// Print stack for panic to log. Fatalf() will let program exit.
				logger.InitLog.Fatalf("panic: %v\n%s", p, string(debug.Stack()))
			}
		}()

		<-signalChannel
		a.Terminate()
		os.Exit(0)
	}()

	oam.AddService(router)
	callback.AddService(router)
	upi.AddService(router)
	for _, serviceName := range factory.SmfConfig.Configuration.ServiceNameList {
		switch models.ServiceName(serviceName) {
		case models.ServiceName_NSMF_PDUSESSION:
			pdusession.AddService(router)
		case models.ServiceName_NSMF_EVENT_EXPOSURE:
			eventexposure.AddService(router)
		}
	}
	udp.Run(pfcp.Dispatch)

	ctx, cancel := context.WithCancel(context.Background())
	smf_context.GetSelf().Ctx = ctx
	smf_context.GetSelf().PFCPCancelFunc = cancel
	for _, upNode := range smf_context.GetSelf().UserPlaneInformation.UPFs {
		upNode.UPF.Ctx, upNode.UPF.CancelFunc = context.WithCancel(context.Background())
		go association.ToBeAssociatedWithUPF(ctx, upNode.UPF)
	}

	time.Sleep(1000 * time.Millisecond)

	HTTPAddr := fmt.Sprintf("%s:%d", smf_context.GetSelf().BindingIPv4, smf_context.GetSelf().SBIPort)
	server, err := httpwrapper.NewHttp2Server(HTTPAddr, tlsKeyLogPath, router)

	if server == nil {
		logger.InitLog.Error("Initialize HTTP server failed:", err)
		return
	}

	if err != nil {
		logger.InitLog.Warnln("Initialize HTTP server:", err)
	}

	serverScheme := factory.SmfConfig.Configuration.Sbi.Scheme
	if serverScheme == "http" {
		err = server.ListenAndServe()
	} else if serverScheme == "https" {
		err = server.ListenAndServeTLS(pemPath, keyPath)
	}

	if err != nil {
		logger.InitLog.Fatalln("HTTP server setup failed:", err)
	}
}

func (a *SmfApp) Terminate() {
	logger.InitLog.Infof("Terminating SMF...")
	// deregister with NRF
	problemDetails, err := consumer.SendDeregisterNFInstance()
	if problemDetails != nil {
		logger.InitLog.Errorf("Deregister NF instance Failed Problem[%+v]", problemDetails)
	} else if err != nil {
		logger.InitLog.Errorf("Deregister NF instance Error[%+v]", err)
	} else {
		logger.InitLog.Infof("Deregister from NRF successfully")
	}
}
