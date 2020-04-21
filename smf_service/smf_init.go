package smf_service

import (
	"bufio"
	"fmt"
	"gofree5gc/lib/http2_util"
	"gofree5gc/lib/openapi/models"
	"gofree5gc/lib/path_util"
	"gofree5gc/lib/pfcp/pfcpUdp"
	"gofree5gc/src/app"
	"gofree5gc/src/smf/EventExposure"
	Nsmf_OAM "gofree5gc/src/smf/OAM"
	"gofree5gc/src/smf/PDUSession"
	"gofree5gc/src/smf/factory"
	"gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/smf_consumer"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_handler"
	"gofree5gc/src/smf/smf_pfcp/pfcp_message"
	"gofree5gc/src/smf/smf_pfcp/pfcp_udp"
	"gofree5gc/src/smf/smf_util"
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
		DefaultSmfConfigPath := path_util.Gofree5gcPath("gofree5gc/config/smfcfg.conf")
		factory.InitConfigFactory(DefaultSmfConfigPath)
	}

	if config.uerouting != "" {
		factory.InitRoutingConfigFactory(config.uerouting)
	} else {
		DefaultUERoutingPath := path_util.Gofree5gcPath("gofree5gc/config/uerouting.yaml")
		factory.InitRoutingConfigFactory(DefaultUERoutingPath)
	}

	initLog.Traceln("SMF debug level(string):", app.ContextSelf().Logger.SMF.DebugLevel)
	if app.ContextSelf().Logger.SMF.DebugLevel != "" {
		initLog.Infoln("SMF debug level(string):", app.ContextSelf().Logger.SMF.DebugLevel)
		level, err := logrus.ParseLevel(app.ContextSelf().Logger.SMF.DebugLevel)
		if err != nil {
			logger.SetLogLevel(level)
		}
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
	smf_context.InitSmfContext(&factory.SmfConfig)
	//allocate id for each upf
	smf_context.AllocateUPFID()
	smf_context.InitSMFUERouting(&factory.UERoutingConfig)

	initLog.Infoln("Server started")
	router := gin.Default()

	err := smf_consumer.SendNFRegistration()

	if err != nil {
		retry_err := smf_consumer.RetrySendNFRegistration(10)
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

	Nsmf_OAM.AddService(router)
	for _, serviceName := range factory.SmfConfig.Configuration.ServiceNameList {
		switch models.ServiceName(serviceName) {
		case models.ServiceName_NSMF_PDUSESSION:
			PDUSession.AddService(router)
		case models.ServiceName_NSMF_EVENT_EXPOSURE:
			EventExposure.AddService(router)
		}
	}
	pfcp_udp.Run()

	for _, upf := range smf_context.SMF_Self().UserPlaneInformation.UPFs {
		addr := new(net.UDPAddr)
		addr.IP = net.IP(upf.NodeID.NodeIdValue)

		addr.Port = pfcpUdp.PFCP_PORT

		logger.AppLog.Infof("Send PFCP Association Request to UPF[%s]\n", addr.String())
		pfcp_message.SendPfcpAssociationSetupRequest(addr)
	}

	time.Sleep(1000 * time.Millisecond)

	go smf_handler.Handle()
	HTTPAddr := fmt.Sprintf("%s:%d", smf_context.SMF_Self().HTTPAddress, smf_context.SMF_Self().HTTPPort)
	server, _ := http2_util.NewServer(HTTPAddr, smf_util.SmfLogPath, router)

	initLog.Infoln(server.ListenAndServeTLS(smf_util.SmfPemPath, smf_util.SmfKeyPath))
}

func (smf *SMF) Terminate() {
	logger.InitLog.Infof("Terminating SMF...")
	// deregister with NRF
	problemDetails, err := smf_consumer.SendDeregisterNFInstance()
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
