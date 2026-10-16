package pfcp

import (
	"fmt"
	"sync"
)

const maxPFCPSequenceNumber uint32 = 0xFFFFFF

// SeqAllocator allocates 24-bit PFCP sequence numbers and keeps outstanding
// requests from sharing the same number. A number is reusable only after Free.
type SeqAllocator struct {
	mu    sync.Mutex
	min   uint32
	max   uint32
	next  uint32
	inUse map[uint32]struct{}
}

func NewSeqAllocator() *SeqAllocator {
	return NewSeqAllocatorRange(0, maxPFCPSequenceNumber)
}

func NewSeqAllocatorRange(minimum, maximum uint32) *SeqAllocator {
	if minimum > maximum || maximum > maxPFCPSequenceNumber {
		panic(fmt.Sprintf("invalid PFCP sequence range [%d,%d]", minimum, maximum))
	}

	return &SeqAllocator{
		min:   minimum,
		max:   maximum,
		next:  minimum,
		inUse: make(map[uint32]struct{}),
	}
}

func (a *SeqAllocator) Allocate() (uint32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	span := a.max - a.min + 1
	for offset := uint32(0); offset < span; offset++ {
		candidate := a.min + (a.next-a.min+offset)%span
		if _, found := a.inUse[candidate]; found {
			continue
		}

		a.inUse[candidate] = struct{}{}
		if candidate == a.max {
			a.next = a.min
		} else {
			a.next = candidate + 1
		}
		return candidate, nil
	}

	return 0, fmt.Errorf("PFCP sequence range [%d,%d] is exhausted", a.min, a.max)
}

func (a *SeqAllocator) Free(sequence uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inUse, sequence)
}
