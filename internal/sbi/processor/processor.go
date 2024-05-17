package processor

import (
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/app"
)

type ProcessorSmf interface {
	app.App
}

type Processor struct {
	ProcessorSmf

	consumer *consumer.Consumer
}

type HandlerResponse struct {
	Status  int
	Headers map[string][]string
	Body    interface{}
}

func NewProcessor(smf ProcessorSmf, consumer *consumer.Consumer) (*Processor, error) {
	p := &Processor{
		ProcessorSmf: smf,
		consumer:     consumer,
	}
	return p, nil
}

func (p *Processor) Consumer() *consumer.Consumer {
	return p.consumer
}
