package processor

import (
	"sync"

	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/app"
)

const (
	CONTEXT_NOT_FOUND = "CONTEXT_NOT_FOUND"
)

type ProcessorSmf interface {
	app.App

	Consumer() *consumer.Consumer
}

type Processor struct {
	ProcessorSmf

	// Pending work is in memory in this stage; each entry is guarded by SMLock.
	chargingWork     sync.Map // map[SMContext.Ref]*chargingWork
	chargingReleases sync.Map // map[SMContext.Ref]*chargingReleaseContinuation
	activePFCPMu     sync.RWMutex
	activePFCPClient ActivePFCPClient
}

func NewProcessor(smf ProcessorSmf) (*Processor, error) {
	p := &Processor{
		ProcessorSmf: smf,
	}
	return p, nil
}
