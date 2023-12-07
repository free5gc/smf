package context

import (
	"fmt"
	"math/rand"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/smf/pkg/factory"
)

func TestUeIPPool(t *testing.T) {
	ueIPPool := NewUEIPPool(&factory.UEIPPool{
		Cidr: "10.10.0.0/24",
	})

	require.NotNil(t, ueIPPool)

	var allocIP net.IP

	// make allowed ip pools
	var ipPoolList []net.IP
	for i := 0; i <= 255; i += 1 {
		ipStr := fmt.Sprintf("10.10.0.%d", i)
		ipPoolList = append(ipPoolList, net.ParseIP(ipStr).To4())
	}

	// allocate
	for i := 0; i < 256; i += 1 {
		allocIP = ueIPPool.allocate(nil)
		require.Contains(t, ipPoolList, allocIP)
	}

	// ip pool is empty
	allocIP = ueIPPool.allocate(nil)
	require.Nil(t, allocIP)

	// release IP
	for _, i := range rand.Perm(256) {
		ueIPPool.release(ipPoolList[i])
	}

	// allocate specify ip
	for _, ip := range ipPoolList {
		allocIP = ueIPPool.allocate(ip)
		require.Equal(t, ip, allocIP)
	}
}

func TestUeIPPool_ExcludeRange(t *testing.T) {
	ueIPPool := NewUEIPPool(&factory.UEIPPool{
		Cidr: "10.10.0.0/24",
	})

	require.Equal(t, 0x0a0a0000, ueIPPool.pool.Min())
	require.Equal(t, 0x0a0a00FF, ueIPPool.pool.Max())
	require.Equal(t, 256, ueIPPool.pool.Remain())

	excludeUeIPPool := NewUEIPPool(&factory.UEIPPool{
		Cidr: "10.10.0.0/28",
	})

	require.Equal(t, 0x0a0a0000, excludeUeIPPool.pool.Min())
	require.Equal(t, 0x0a0a000F, excludeUeIPPool.pool.Max())

	require.Equal(t, 16, excludeUeIPPool.pool.Remain())

	err := ueIPPool.exclude(excludeUeIPPool)
	require.NoError(t, err)
	require.Equal(t, 240, ueIPPool.pool.Remain())

	for i := 16; i <= 255; i++ {
		allocate := ueIPPool.allocate(nil)
		require.Equal(t, net.ParseIP(fmt.Sprintf("10.10.0.%d", i)).To4(), allocate)

		ueIPPool.release(allocate)
	}
}
