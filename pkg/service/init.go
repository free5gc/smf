package service

import (
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	aperLogger "github.com/free5gc/aper/logger"
	nasLogger "github.com/free5gc/nas/logger"
	ngapLogger "github.com/free5gc/ngap/logger"
	"github.com/free5gc/openapi/models"
	pfcpLogger "github.com/free5gc/pfcp/logger"
	"github.com/free5gc/pfcp/pfcpType"
	"github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/pfcp"
	"github.com/free5gc/smf/internal/pfcp/udp"
	"github.com/free5gc/smf/internal/sbi/callback"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/internal/sbi/eventexposure"
	"github.com/free5gc/smf/internal/sbi/oam"
	"github.com/free5gc/smf/internal/sbi/pdusession"
	"github.com/free5gc/smf/internal/util"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/httpwrapper"
	logger_util "github.com/free5gc/util/logger"
)

type SMF struct {
	KeyLogPath string
}

type (
	// Commands information.
	Commands struct {
		config    string
		uerouting string
	}
)

var commands Commands

var cliCmd = []cli.Flag{
	cli.StringFlag{
		Name:  "config, c",
		Usage: "Load configuration from `FILE`",
	},
	cli.StringFlag{
		Name:  "log, l",
		Usage: "Output NF log to `FILE`",
	},
	cli.StringFlag{
		Name:  "log5gc, lc",
		Usage: "Output free5gc log to `FILE`",
	},
	cli.StringFlag{
		Name:  "uerouting, u",
		Usage: "Load UE routing configuration from `FILE`",
	},
}

func (*SMF) GetCliCmd() (flags []cli.Flag) {
	return cliCmd
}

func (smf *SMF) Initialize(c *cli.Context) error {
	commands = Commands{
		config:    c.String("config"),
		uerouting: c.String("uerouting"),
	}

	if commands.config != "" {
		if err := factory.InitConfigFactory(commands.config); err != nil {
			return err
		}
	} else {
		if err := factory.InitConfigFactory(util.SmfDefaultConfigPath); err != nil {
			return err
		}
	}

	if commands.uerouting != "" {
		if err := factory.InitRoutingConfigFactory(commands.uerouting); err != nil {
			return err
		}
	} else {
		if err := factory.InitRoutingConfigFactory(util.SmfDefaultUERoutingPath); err != nil {
			return err
		}
	}

	if err := factory.CheckConfigVersion(); err != nil {
		return err
	}

	if _, err := factory.SmfConfig.Validate(); err != nil {
		return err
	}

	if _, err := factory.UERoutingConfig.Validate(); err != nil {
		return err
	}

	smf.SetLogLevel()

	return nil
}

func (smf *SMF) SetLogLevel() {
	if factory.SmfConfig.Logger == nil {
		logger.InitLog.Warnln("SMF config without log level setting!!!")
		return
	}

	if factory.SmfConfig.Logger.SMF != nil {
		if factory.SmfConfig.Logger.SMF.DebugLevel != "" {
			if level, err := logrus.ParseLevel(factory.SmfConfig.Logger.SMF.DebugLevel); err != nil {
				logger.InitLog.Warnf("SMF Log level [%s] is invalid, set to [info] level",
					factory.SmfConfig.Logger.SMF.DebugLevel)
				logger.SetLogLevel(logrus.InfoLevel)
			} else {
				logger.InitLog.Infof("SMF Log level is set to [%s] level", level)
				logger.SetLogLevel(level)
			}
		} else {
			logger.InitLog.Infoln("SMF Log level is default set to [info] level")
			logger.SetLogLevel(logrus.InfoLevel)
		}
		logger.SetReportCaller(factory.SmfConfig.Logger.SMF.ReportCaller)
	}

	if factory.SmfConfig.Logger.NAS != nil {
		if factory.SmfConfig.Logger.NAS.DebugLevel != "" {
			if level, err := logrus.ParseLevel(factory.SmfConfig.Logger.NAS.DebugLevel); err != nil {
				nasLogger.NasLog.Warnf("NAS Log level [%s] is invalid, set to [info] level",
					factory.SmfConfig.Logger.NAS.DebugLevel)
				logger.SetLogLevel(logrus.InfoLevel)
			} else {
				nasLogger.SetLogLevel(level)
			}
		} else {
			nasLogger.NasLog.Warnln("NAS Log level not set. Default set to [info] level")
			nasLogger.SetLogLevel(logrus.InfoLevel)
		}
		nasLogger.SetReportCaller(factory.SmfConfig.Logger.NAS.ReportCaller)
	}

	if factory.SmfConfig.Logger.NGAP != nil {
		if factory.SmfConfig.Logger.NGAP.DebugLevel != "" {
			if level, err := logrus.ParseLevel(factory.SmfConfig.Logger.NGAP.DebugLevel); err != nil {
				ngapLogger.NgapLog.Warnf("NGAP Log level [%s] is invalid, set to [info] level",
					factory.SmfConfig.Logger.NGAP.DebugLevel)
				ngapLogger.SetLogLevel(logrus.InfoLevel)
			} else {
				ngapLogger.SetLogLevel(level)
			}
		} else {
			ngapLogger.NgapLog.Warnln("NGAP Log level not set. Default set to [info] level")
			ngapLogger.SetLogLevel(logrus.InfoLevel)
		}
		ngapLogger.SetReportCaller(factory.SmfConfig.Logger.NGAP.ReportCaller)
	}

	if factory.SmfConfig.Logger.Aper != nil {
		if factory.SmfConfig.Logger.Aper.DebugLevel != "" {
			if level, err := logrus.ParseLevel(factory.SmfConfig.Logger.Aper.DebugLevel); err != nil {
				aperLogger.AperLog.Warnf("Aper Log level [%s] is invalid, set to [info] level",
					factory.SmfConfig.Logger.Aper.DebugLevel)
				aperLogger.SetLogLevel(logrus.InfoLevel)
			} else {
				aperLogger.SetLogLevel(level)
			}
		} else {
			aperLogger.AperLog.Warnln("Aper Log level not set. Default set to [info] level")
			aperLogger.SetLogLevel(logrus.InfoLevel)
		}
		aperLogger.SetReportCaller(factory.SmfConfig.Logger.Aper.ReportCaller)
	}

	if factory.SmfConfig.Logger.PFCP != nil {
		if factory.SmfConfig.Logger.PFCP.DebugLevel != "" {
			if level, err := logrus.ParseLevel(factory.SmfConfig.Logger.PFCP.DebugLevel); err != nil {
				pfcpLogger.PFCPLog.Warnf("PFCP Log level [%s] is invalid, set to [info] level",
					factory.SmfConfig.Logger.PFCP.DebugLevel)
				pfcpLogger.SetLogLevel(logrus.InfoLevel)
			} else {
				pfcpLogger.SetLogLevel(level)
			}
		} else {
			pfcpLogger.PFCPLog.Warnln("PFCP Log level not set. Default set to [info] level")
			pfcpLogger.SetLogLevel(logrus.InfoLevel)
		}
		pfcpLogger.SetReportCaller(factory.SmfConfig.Logger.PFCP.ReportCaller)
	}
}

func (smf *SMF) FilterCli(c *cli.Context) (args []string) {
	for _, flag := range smf.GetCliCmd() {
		name := flag.GetName()
		value := fmt.Sprint(c.Generic(name))
		if value == "" {
			continue
		}

		args = append(args, "--"+name, value)
	}
	return args
}

func (smf *SMF) Start() {
	pemPath := util.SmfDefaultPemPath
	keyPath := util.SmfDefaultKeyPath
	sbi := factory.SmfConfig.Configuration.Sbi
	if sbi.Tls != nil {
		pemPath = sbi.Tls.Pem
		keyPath = sbi.Tls.Key
	}

	context.InitSmfContext(&factory.SmfConfig)
	// allocate id for each upf
	context.AllocateUPFID()
	context.InitSMFUERouting(&factory.UERoutingConfig)

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
		smf.Terminate()
		os.Exit(0)
	}()

	oam.AddService(router)
	callback.AddService(router)
	for _, serviceName := range factory.SmfConfig.Configuration.ServiceNameList {
		switch models.ServiceName(serviceName) {
		case models.ServiceName_NSMF_PDUSESSION:
			pdusession.AddService(router)
		case models.ServiceName_NSMF_EVENT_EXPOSURE:
			eventexposure.AddService(router)
		}
	}
	udp.Run(pfcp.Dispatch)

	for _, upf := range context.SMF_Self().UserPlaneInformation.UPFs {
		var upfStr string
		if upf.NodeID.NodeIdType == pfcpType.NodeIdTypeFqdn {
			upfStr = fmt.Sprintf("[%s](%s)", upf.NodeID.FQDN, upf.NodeID.ResolveNodeIdToIp().String())
		} else {
			upfStr = fmt.Sprintf("[%s]", upf.NodeID.IP.String())
		}
		if err = setupPfcpAssociation(upf.UPF, upfStr); err != nil {
			logger.AppLog.Errorf("Failed to setup an association with UPF%s, error:%+v", upfStr, err)
		}
	}

	time.Sleep(1000 * time.Millisecond)

	HTTPAddr := fmt.Sprintf("%s:%d", context.SMF_Self().BindingIPv4, context.SMF_Self().SBIPort)
	server, err := httpwrapper.NewHttp2Server(HTTPAddr, smf.KeyLogPath, router)

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

func (smf *SMF) Terminate() {
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

func (smf *SMF) Exec(c *cli.Context) error {
	return nil
}
