//+build !debug

package smf_util

import (
	"gofree5gc/lib/path_util"
)

var SmfLogPath = path_util.Gofree5gcPath("gofree5gc/smfsslkey.log")
var SmfPemPath = path_util.Gofree5gcPath("gofree5gc/support/TLS/smf.pem")
var SmfKeyPath = path_util.Gofree5gcPath("gofree5gc/support/TLS/smf.key")
var DefaultSmfConfigPath = path_util.Gofree5gcPath("gofree5gc/config/smfcfg.conf")
