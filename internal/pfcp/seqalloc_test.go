package pfcp_test

import (
	"testing"

	"github.com/free5gc/smf/internal/pfcp"
)

func TestSeqAllocatorStaysWithin24Bits(t *testing.T) {
	a := pfcp.NewSeqAllocator()
	for i := 0; i < 1000; i++ {
		seq, err := a.Allocate()
		if err != nil {
			t.Fatalf("Allocate() error: %v", err)
		}
		if seq > 0xFFFFFF {
			t.Fatalf("sequence %#x exceeds the 24-bit range", seq)
		}
	}
}

func TestSeqAllocatorFreeAllowsReuse(t *testing.T) {
	a := pfcp.NewSeqAllocatorRange(0, 2)
	s1, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Allocate(); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Allocate(); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Allocate(); err == nil {
		t.Fatal("expected an exhaustion error")
	}

	a.Free(s1)
	reused, err := a.Allocate()
	if err != nil {
		t.Fatalf("Allocate() after Free() error: %v", err)
	}
	if reused != s1 {
		t.Fatalf("Allocate() after Free() = %d, want %d", reused, s1)
	}
}

func TestSeqAllocatorWrapsAndSkipsInUseIDs(t *testing.T) {
	a := pfcp.NewSeqAllocatorRange(5, 6)
	first, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	a.Free(first)

	got, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("Allocate() after wrap = %d, want freed ID %d (other ID %d is still in use)", got, first, second)
	}
}
