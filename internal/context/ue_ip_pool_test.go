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
	for i := 1; i < 255; i += 1 {
		ipStr := fmt.Sprintf("10.10.0.%d", i)
		ipPoolList = append(ipPoolList, net.ParseIP(ipStr).To4())
	}

	// allocate
	for i := 1; i < 255; i += 1 {
		allocIP = ueIPPool.allocate(nil)
		require.Contains(t, ipPoolList, allocIP)
	}

	// ip pool is empty
	allocIP = ueIPPool.allocate(nil)
	require.Nil(t, allocIP)

	// release IP
	for _, i := range rand.Perm(254) {
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

	require.Equal(t, 0x0a0a0001, ueIPPool.pool.Min())
	require.Equal(t, 0x0a0a00FE, ueIPPool.pool.Max())
	require.Equal(t, 254, ueIPPool.pool.Remain())

	excludeUeIPPool := NewUEIPPool(&factory.UEIPPool{
		Cidr: "10.10.0.0/28",
	})

	require.Equal(t, 0x0a0a0001, excludeUeIPPool.pool.Min())
	require.Equal(t, 0x0a0a000E, excludeUeIPPool.pool.Max())

	require.Equal(t, 14, excludeUeIPPool.pool.Remain())

	err := ueIPPool.exclude(excludeUeIPPool)
	require.NoError(t, err)
	require.Equal(t, 239, ueIPPool.pool.Remain())

	for i := 16; i <= 254; i++ {
		allocate := ueIPPool.allocate(nil)
		require.Equal(t, net.ParseIP(fmt.Sprintf("10.10.0.%d", i)).To4(), allocate)

		ueIPPool.release(allocate)
	}
}
