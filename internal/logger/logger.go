package logger

import (
	"os"
	"time"

	formatter "github.com/antonfisher/nested-logrus-formatter"
	"github.com/sirupsen/logrus"

	aperLogger "github.com/free5gc/aper/logger"
	nasLogger "github.com/free5gc/nas/logger"
	ngapLogger "github.com/free5gc/ngap/logger"
	pfcpLogger "github.com/free5gc/pfcp/logger"
	logger_util "github.com/free5gc/util/logger"
)

const (
	FieldSupi         = "supi"
	FieldPDUSessionID = "pdu_session_id"
)

var (
	log         *logrus.Logger
	AppLog      *logrus.Entry
	InitLog     *logrus.Entry
	CfgLog      *logrus.Entry
	GsmLog      *logrus.Entry
	PfcpLog     *logrus.Entry
	PduSessLog  *logrus.Entry
	CtxLog      *logrus.Entry
	ConsumerLog *logrus.Entry
	GinLog      *logrus.Entry
)

func init() {
	log = logrus.New()
	log.SetReportCaller(false)

	log.Formatter = &formatter.Formatter{
		TimestampFormat: time.RFC3339,
		TrimMessages:    true,
		NoFieldsSpace:   true,
		HideKeys:        true,
		FieldsOrder:     []string{"component", "category"},
	}

	AppLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "App"})
	InitLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "Init"})
	CfgLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "CFG"})
	PfcpLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "PFCP"})
	PduSessLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "PduSess"})
	GsmLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "GSM"})
	CtxLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "CTX"})
	ConsumerLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "Consumer"})
	GinLog = log.WithFields(logrus.Fields{"component": "SMF", "category": "GIN"})
}

func LogFileHook(logNfPath string, log5gcPath string) error {
	if fullPath, err := logger_util.CreateFree5gcLogFile(log5gcPath); err == nil {
		if fullPath != "" {
			free5gcLogHook, hookErr := logger_util.NewFileHook(fullPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o666)
			if hookErr != nil {
				return hookErr
			}
			log.Hooks.Add(free5gcLogHook)
			aperLogger.GetLogger().Hooks.Add(free5gcLogHook)
			ngapLogger.GetLogger().Hooks.Add(free5gcLogHook)
			nasLogger.GetLogger().Hooks.Add(free5gcLogHook)
			pfcpLogger.GetLogger().Hooks.Add(free5gcLogHook)
		}
	} else {
		return err
	}

	if fullPath, err := logger_util.CreateNfLogFile(logNfPath, "smf.log"); err == nil {
		selfLogHook, hookErr := logger_util.NewFileHook(fullPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o666)
		if hookErr != nil {
			return hookErr
		}
		log.Hooks.Add(selfLogHook)
		aperLogger.GetLogger().Hooks.Add(selfLogHook)
		ngapLogger.GetLogger().Hooks.Add(selfLogHook)
		nasLogger.GetLogger().Hooks.Add(selfLogHook)
		pfcpLogger.GetLogger().Hooks.Add(selfLogHook)
	} else {
		return err
	}

	return nil
}

func SetLogLevel(level logrus.Level) {
	log.SetLevel(level)
}

func SetReportCaller(enable bool) {
	log.SetReportCaller(enable)
}
