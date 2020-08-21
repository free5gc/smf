package service

import (
	"bufio"
	"fmt"
	"free5gc/lib/http2_util"
	"free5gc/lib/openapi/models"
	"free5gc/lib/path_util"
	"free5gc/lib/pfcp/pfcpUdp"
	"free5gc/src/app"
	"free5gc/src/smf/callback"
	"free5gc/src/smf/consumer"
	"free5gc/src/smf/context"
	"free5gc/src/smf/eventexposure"
	"free5gc/src/smf/factory"
	"free5gc/src/smf/handler"
	"free5gc/src/smf/logger"
	"free5gc/src/smf/oam"
	"free5gc/src/smf/pdusession"
	"free5gc/src/smf/pfcp/message"
	"free5gc/src/smf/pfcp/udp"
	"free5gc/src/smf/util"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

type SMF struct{}

type (
	// Config information.
	Config struct {
		smfcfg    string
		uerouting string
	}
)

var config Config

var smfCLi = []cli.Flag{
	cli.StringFlag{
		Name:  "free5gccfg",
		Usage: "common config file",
	},
	cli.StringFlag{
		Name:  "smfcfg",
		Usage: "config file",
	},
	cli.StringFlag{
		Name:  "uerouting",
		Usage: "config file",
	},
}

var initLog *logrus.Entry

func init() {
	initLog = logger.InitLog
}

func (*SMF) GetCliCmd() (flags []cli.Flag) {
	return smfCLi
}

func (*SMF) Initialize(c *cli.Context) {

	config = Config{
		smfcfg:    c.String("smfcfg"),
		uerouting: c.String("uerouting"),
	}

	if config.smfcfg != "" {
		factory.InitConfigFactory(config.smfcfg)
	} else {
		DefaultSmfConfigPath := path_util.Gofree5gcPath("free5gc/config/smfcfg.conf")
		factory.InitConfigFactory(DefaultSmfConfigPath)
	}

	if config.uerouting != "" {
		factory.InitRoutingConfigFactory(config.uerouting)
	} else {
		DefaultUERoutingPath := path_util.Gofree5gcPath("free5gc/config/uerouting.yaml")
		factory.InitRoutingConfigFactory(DefaultUERoutingPath)
	}

	if app.ContextSelf().Logger.SMF.DebugLevel != "" {
		level, err := logrus.ParseLevel(app.ContextSelf().Logger.SMF.DebugLevel)
		if err != nil {
			initLog.Warnf("Log level [%s] is not valid, set to [info] level", app.ContextSelf().Logger.SMF.DebugLevel)
			logger.SetLogLevel(logrus.InfoLevel)
		} else {
			logger.SetLogLevel(level)
			initLog.Infof("Log level is set to [%s] level", level)
		}
	} else {
		initLog.Infoln("Log level is default set to [info] level")
		logger.SetLogLevel(logrus.InfoLevel)
	}

	logger.SetReportCaller(app.ContextSelf().Logger.SMF.ReportCaller)
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
	context.InitSmfContext(&factory.SmfConfig)
	//allocate id for each upf
	context.AllocateUPFID()
	context.InitSMFUERouting(&factory.UERoutingConfig)

	initLog.Infoln("Server started")
	router := gin.Default()

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
	udp.Run()

	for _, upf := range context.SMF_Self().UserPlaneInformation.UPFs {
		addr := new(net.UDPAddr)
		addr.IP = net.IP(upf.NodeID.NodeIdValue)

		addr.Port = pfcpUdp.PFCP_PORT

		logger.AppLog.Infof("Send PFCP Association Request to UPF[%s]\n", addr.String())
		message.SendPfcpAssociationSetupRequest(addr)
	}

	time.Sleep(1000 * time.Millisecond)

	go handler.Handle()
	HTTPAddr := fmt.Sprintf("%s:%d", context.SMF_Self().BindingIPv4, context.SMF_Self().HTTPPort)
	server, err := http2_util.NewServer(HTTPAddr, util.SmfLogPath, router)

	if server == nil {
		initLog.Errorln("Initialize HTTP server failed: %+v", err)
		return
	}

	if err != nil {
		initLog.Warnln("Initialize HTTP server: +%v", err)
	}

	serverScheme := factory.SmfConfig.Configuration.Sbi.Scheme
	if serverScheme == "http" {
		err = server.ListenAndServe()
	} else if serverScheme == "https" {
		err = server.ListenAndServeTLS(util.SmfPemPath, util.SmfKeyPath)
	}

	if err != nil {
		initLog.Fatalln("HTTP server setup failed: %+v", err)
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
	initLog.Traceln("args:", c.String("smfcfg"))
	args := smf.FilterCli(c)
	initLog.Traceln("filter: ", args)
	command := exec.Command("./smf", args...)

	stdout, err := command.StdoutPipe()
	if err != nil {
		initLog.Fatalln(err)
	}
	wg := sync.WaitGroup{}
	wg.Add(3)
	go func() {
		in := bufio.NewScanner(stdout)
		for in.Scan() {
			fmt.Println(in.Text())
		}
		wg.Done()
	}()

	stderr, err := command.StderrPipe()
	if err != nil {
		initLog.Fatalln(err)
	}
	go func() {
		in := bufio.NewScanner(stderr)
		for in.Scan() {
			fmt.Println(in.Text())
		}
		wg.Done()
	}()

	go func() {
		if err := command.Start(); err != nil {
			initLog.Errorf("SMF Start error: %v", err)
		}
		wg.Done()
	}()

	wg.Wait()

	return err
}
