package util

import (
	"errors"
	"strconv"
	"strings"

	"github.com/free5gc/ngap/ngapType"
	"github.com/free5gc/smf/internal/logger"
)

func BitRateTokbps(bitrate string) (uint64, error) {
	s := strings.Split(bitrate, " ")
	var kbps uint64

	var digit uint64

	if bitrate == "" {
		logger.UtilLog.Debugf("BitRateTokbps: bitrate is empty string, returning 0")
		return 0, nil
	}

	if len(s) < 2 {
		return 0, errors.New("invalid bitrate format")
	}

	if f, err := strconv.ParseFloat(s[0], 64); err != nil {
		return 0, errors.New("invalid bitrate value")
	} else {
		digit = uint64(f)
	}

	switch s[1] {
	case "bps":
		kbps = digit / 1000
	case "Kbps":
		kbps = digit * 1
	case "Mbps":
		kbps = digit * 1000
	case "Gbps":
		kbps = digit * 1000000
	case "Tbps":
		kbps = digit * 1000000000
	default:
		logger.UtilLog.Errorf("BitRateTokbps: unknown unit: '%s' in bitrate: '%s'", s[1], bitrate)
	}
	logger.UtilLog.Debugf("BitRateTokbps: input='%s' -> digit=%d, unit='%s', result kbps=%d", bitrate, digit, s[1], kbps)
	return kbps, nil
}

func BitRateTombps(bitrate string) uint16 {
	s := strings.Split(bitrate, " ")
	var mbps uint16

	var digit int

	if n, err := strconv.Atoi(s[0]); err != nil {
		return 0
	} else {
		digit = n
	}

	switch s[1] {
	case "bps":
		mbps = uint16(digit / 1000000)
	case "Kbps":
		mbps = uint16(digit / 1000)
	case "Mbps":
		mbps = uint16(digit * 1)
	case "Gbps":
		mbps = uint16(digit * 1000)
	case "Tbps":
		mbps = uint16(digit * 1000000)
	}
	return mbps
}

func StringToBitRate(bitrate string) ngapType.BitRate {
	s := strings.Split(bitrate, " ")

	var digit int

	if n, err := strconv.Atoi(s[0]); err != nil {
		return ngapType.BitRate{Value: 0}
	} else {
		digit = n
	}
	switch s[1] {
	case "bps":
		// no need to modify
	case "Kbps":
		digit = (digit * 1000)
	case "Mbps":
		digit = (digit * 1000000)
	case "Gbps":
		digit = (digit * 1000000000)
	case "Tbps":
		digit = (digit * 1000000000000)
	}

	return ngapType.BitRate{Value: int64(digit)}
}
