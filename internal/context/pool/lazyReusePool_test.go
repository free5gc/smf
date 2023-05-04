package pool

import (
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLazyReusePool(t *testing.T) {
	// OK : first < last
	lrp, err := NewLazyReusePool(10, 100)
	assert.NoError(t, err)
	assert.NotEmpty(t, lrp)
	assert.Equal(t, 91, lrp.Total())
	assert.Equal(t, 91, lrp.Remain())

	// OK : first == last
	lrp, err = NewLazyReusePool(100, 100)
	assert.NoError(t, err)
	assert.NotEmpty(t, lrp)
	assert.Equal(t, 1, lrp.Total())
	assert.Equal(t, 1, lrp.Remain())

	// NG : first > last
	lrp, err = NewLazyReusePool(10, 0)
	assert.Empty(t, lrp)
	assert.Error(t, err)
}

func TestLazyReusePool_SingleSegment(t *testing.T) {
	// Allocation OK
	p, err := NewLazyReusePool(1, 2)
	assert.NoError(t, err)
	a, ok := p.Allocate()
	assert.Equal(t, 1, a)
	assert.True(t, ok)
	assert.Equal(t, 1, p.Remain())
	a, ok = p.Allocate()
	assert.Equal(t, a, 2)
	assert.True(t, ok)
	assert.Equal(t, 0, p.Remain())

	// exhauted
	_, ok = p.Allocate()
	assert.False(t, ok)
	assert.Equal(t, 0, p.Remain())

	// free 1
	ok = p.Free(1)
	assert.True(t, ok)
	assert.Equal(t, 1, p.head.first)
	assert.Equal(t, 1, p.head.last)
	assert.Empty(t, p.head.next)
	assert.Equal(t, 1, p.Remain())

	// out of range
	ok = p.Free(0)
	assert.False(t, ok)

	// duplecated free
	ok = p.Free(1)
	assert.False(t, ok)

	// free 2
	ok = p.Free(2)
	assert.True(t, ok)
	assert.Equal(t, 2, p.Remain())

	// reuse
	a, ok = p.Allocate()
	assert.Equal(t, 1, a)
	assert.True(t, ok)
	assert.Equal(t, 1, p.Remain())
	assert.Equal(t, 2, p.Total())

	ok = p.Free(1)
	assert.True(t, ok)
	assert.Equal(t, 2, p.Remain())

	ok = p.Use(2)
	assert.True(t, ok)
	assert.Equal(t, 1, p.Remain())

	ok = p.Use(1)
	assert.True(t, ok)
	assert.Equal(t, 0, p.Remain())
	assert.Equal(t, 2, p.Total())
}

func TestLazyReusePool_ManySegment(t *testing.T) {
	p, err := NewLazyReusePool(1, 100)
	assert.NoError(t, err)
	assert.Equal(t, 100, p.Remain())

	// -> 1-100

	for i := 0; i < 99; i++ {
		_, ok := p.Allocate()
		assert.True(t, ok)
	}

	// -> 100-100
	assert.Equal(t, 1, p.Remain())

	p.Free(3) // -> 100-100 -> 3-3
	assert.Equal(t, 3, p.head.next.first)
	assert.Equal(t, 3, p.head.next.last)

	p.Free(6) // -> 100-100 -> 3-3 -> 6-6
	assert.Equal(t, 6, p.head.next.next.first)
	assert.Equal(t, 6, p.head.next.next.last)
	assert.Equal(t, 3, p.Remain())

	// adjacent to the front
	p.Free(2) // -> 100-100 -> 2-3 -> 6-6
	assert.Equal(t, 2, p.head.next.first)
	assert.Equal(t, 3, p.head.next.last)
	assert.Equal(t, 4, p.Remain())

	// duplicate
	ok := p.Free(3)
	assert.False(t, ok)

	// adjacent to the back
	p.Free(4) // -> 100-100 -> 2-4 -> 6-6
	assert.Equal(t, 2, p.head.next.first)
	assert.Equal(t, 4, p.head.next.last)
	assert.Equal(t, 5, p.Remain())

	// 3rd segment
	p.Free(7) // -> 100-100 -> 2-4 -> 6-7
	assert.Equal(t, 6, p.head.next.next.first)
	assert.Equal(t, 7, p.head.next.next.last)
	assert.Equal(t, 6, p.Remain())

	// concatenate
	p.Free(5) // -> 100-100 -> 2-7
	assert.Equal(t, 2, p.head.next.first)
	assert.Equal(t, 7, p.head.next.last)
	assert.Empty(t, p.head.next.next)
	assert.Equal(t, 7, p.Remain())

	// new head
	a, ok := p.Allocate() // -> 2-7
	assert.Equal(t, a, 100)
	assert.True(t, ok)
	assert.Equal(t, 2, p.head.first)
	assert.Equal(t, 7, p.head.last)
	assert.Equal(t, 6, p.Remain())

	// reuse
	a, ok = p.Allocate() // -> 3-7
	assert.Equal(t, a, 2)
	assert.True(t, ok)
	assert.Equal(t, 3, p.head.first)
	assert.Equal(t, 7, p.head.last)
	assert.Empty(t, p.head.next)
	assert.Equal(t, 5, p.Remain())

	// return to head
	p.Free(100) // -> 3-7 -> 100-100
	p.Free(8)   // -> 3-8 -> 100-100
	assert.Equal(t, 3, p.head.first)
	assert.Equal(t, 8, p.head.last)
	assert.Equal(t, 100, p.head.next.first)
	assert.Equal(t, 100, p.head.next.last)
	assert.Equal(t, 7, p.Remain())

	// return into between head and 2nd
	p.Free(10) // -> 3-8 -> 10-10 -> 100-100
	assert.Equal(t, 3, p.head.first)
	assert.Equal(t, 8, p.head.last)
	assert.Equal(t, 10, p.head.next.first)
	assert.Equal(t, 10, p.head.next.last)
	assert.Equal(t, 100, p.head.next.next.first)
	assert.Equal(t, 100, p.head.next.next.last)
	assert.Equal(t, 8, p.Remain())

	// concatenate head and 2nd
	p.Free(9) // -> 3-10 -> 100-100
	assert.Equal(t, 3, p.head.first)
	assert.Equal(t, 10, p.head.last)
	assert.Equal(t, 100, p.head.next.first)
	assert.Equal(t, 100, p.head.next.last)
	assert.Empty(t, p.head.next.next)
	assert.Equal(t, 9, p.Remain())

	ok = p.Use(100) // 3-10
	assert.True(t, ok)
	assert.Equal(t, 3, p.head.first)
	assert.Equal(t, 10, p.head.last)
	assert.Nil(t, p.head.next)
	assert.Equal(t, 8, p.Remain())

	ok = p.Use(3) // 4-10
	assert.True(t, ok)
	assert.Equal(t, 4, p.head.first)
	assert.Equal(t, 10, p.head.last)
	assert.Nil(t, p.head.next)
	assert.Equal(t, 7, p.Remain())

	ok = p.Use(7) // 4-6 -> 8-10
	assert.True(t, ok)
	assert.Equal(t, 4, p.head.first)
	assert.Equal(t, 6, p.head.last)
	assert.Equal(t, 8, p.head.next.first)
	assert.Equal(t, 10, p.head.next.last)
	assert.Equal(t, 6, p.Remain())

	p.Free(7) // 4-10
	assert.Equal(t, 4, p.head.first)
	assert.Equal(t, 10, p.head.last)
	assert.Nil(t, p.head.next)

	p.Free(3) // 4-10 -> 3
	assert.Equal(t, 4, p.head.first)
	assert.Equal(t, 10, p.head.last)
	assert.Equal(t, 3, p.head.next.first)
	assert.Equal(t, 3, p.head.next.last)

	p.Free(100) // 4-10 -> 3 -> 100
	assert.Equal(t, 4, p.head.first)
	assert.Equal(t, 10, p.head.last)
	assert.Equal(t, 3, p.head.next.first)
	assert.Equal(t, 3, p.head.next.last)
	assert.Equal(t, 100, p.head.next.next.first)
	assert.Equal(t, 100, p.head.next.next.last)
}

func TestLazyReusePool_ReserveSection(t *testing.T) {
	p, err := NewLazyReusePool(1, 100)
	require.NoError(t, err)
	require.Equal(t, 100, p.Remain())

	err = p.Reserve(30, 69)
	require.NoError(t, err)
	require.Equal(t, 60, p.Remain())

	var allocated []int
	for {
		a, ok := p.Allocate()
		if ok {
			allocated = append(allocated, a)
		} else {
			break
		}
	}
	var expected []int
	for i := 1; i <= 29; i++ {
		expected = append(expected, i)
	}
	for i := 70; i <= 100; i++ {
		expected = append(expected, i)
	}

	require.Equal(t, expected, allocated)
}

func TestLazyReusePool_ManyGoroutine(t *testing.T) {
	p, err := NewLazyReusePool(101, 1000)
	assert.NoError(t, err)
	assert.Equal(t, 900, p.Remain())
	ch := make(chan int)

	numOfThreads := 400

	for i := 0; i < numOfThreads; i++ {
		// Allocate 2 times and Free 1 time
		go func() {
			defer func() {
				if p := recover(); p != nil {
					// Program will be exited.
					fmt.Fprintf(os.Stderr, "panic: %v\n%s", p, string(debug.Stack()))
					os.Exit(1)
				}
			}()

			a1, ok := p.Allocate()
			assert.True(t, ok)
			ch <- a1

			time.Sleep(10 * time.Millisecond)

			ok = p.Free(a1)
			assert.True(t, ok)

			time.Sleep(10 * time.Millisecond)

			a2, ok := p.Allocate()
			assert.True(t, ok)
			ch <- a2
		}()
	}
	// collect allocated values
	allocated := make([]int, 0, numOfThreads*2)
	for i := 0; i < numOfThreads*2; i++ {
		allocated = append(allocated, <-ch)
	}
	sort.Ints(allocated)

	expected := make([]int, numOfThreads*2)
	for i := 0; i < numOfThreads*2; i++ {
		expected[i] = p.first + i
	}
	assert.Equal(t, expected, allocated)
	assert.Equal(t, 900-numOfThreads, p.Remain())

	a, ok := p.Allocate()
	assert.Equal(t, p.first+numOfThreads*2, a)
	assert.True(t, ok)
	assert.Equal(t, 900-numOfThreads-1, p.Remain())
}
