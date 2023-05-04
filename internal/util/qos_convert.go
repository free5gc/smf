package util

import (
	"strconv"
	"strings"

	"github.com/free5gc/ngap/ngapType"
)

func BitRateTokbps(bitrate string) uint64 {
	s := strings.Split(bitrate, " ")
	var kbps uint64

	var digit int

	if n, err := strconv.Atoi(s[0]); err != nil {
		return 0
	} else {
		digit = n
	}

	switch s[1] {
	case "bps":
		kbps = uint64(digit / 1000)
	case "Kbps":
		kbps = uint64(digit * 1)
	case "Mbps":
		kbps = uint64(digit * 1000)
	case "Gbps":
		kbps = uint64(digit * 1000000)
	case "Tbps":
		kbps = uint64(digit * 1000000000)
	}
	return kbps
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
