package context

import (
	"errors"
	"net"

	"github.com/free5gc/idgenerator"
)

type IPAllocator struct {
	ipNetwork *net.IPNet
	g         *idgenerator.IDGenerator
}

func NewIPAllocator(cidr string) (*IPAllocator, error) {
	allocator := &IPAllocator{}

	if _, ipnet, err := net.ParseCIDR(cidr); err != nil {
		return nil, err
	} else {
		allocator.ipNetwork = ipnet
	}
	allocator.g = idgenerator.NewGenerator(1, 1<<int64(32-maskBits(allocator.ipNetwork.Mask))-2)

	return allocator, nil
}

func maskBits(mask net.IPMask) int {
	var cnt int
	for _, b := range mask {
		for ; b != 0; b /= 2 {
			if b%2 != 0 {
				cnt++
			}
		}
	}
	return cnt
}

// IPAddrWithOffset add offset on base ip
func IPAddrWithOffset(ip net.IP, offset int) net.IP {
	retIP := make(net.IP, len(ip))
	copy(retIP, ip)

	var carry int
	for i := len(retIP) - 1; i >= 0; i-- {
		if offset == 0 {
			break
		}

		val := int(retIP[i]) + carry + offset%256
		retIP[i] = byte(val % 256)
		carry = val / 256

		offset /= 256
	}

	return retIP
}

// IPAddrOffset calculate the input ip with base ip offset
func IPAddrOffset(in, base net.IP) int {
	offset := 0
	exp := 1
	for i := len(base) - 1; i >= 0; i-- {
		offset += int(in[i]-base[i]) * exp
		exp *= 256
	}
	return offset
}

// Allocate will allocate the IP address and returns it
func (a *IPAllocator) Allocate() (net.IP, error) {
	if offset, err := a.g.Allocate(); err != nil {
		return nil, errors.New("ip allocation failed" + err.Error())
	} else {
		return IPAddrWithOffset(a.ipNetwork.IP, int(offset)), nil
	}
}

func (a *IPAllocator) Release(ip net.IP) {
	offset := IPAddrOffset(ip, a.ipNetwork.IP)
	a.g.FreeID(int64(offset))
}
