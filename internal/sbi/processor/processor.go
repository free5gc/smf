package processor

import (
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/smf/pkg/app"
)

type Processor struct {
	app.App

	consumer *consumer.Consumer
}

type HandlerResponse struct {
	Status  int
	Headers map[string][]string
	Body    interface{}
}

func NewProcessor(smf app.App, consumer *consumer.Consumer) (*Processor, error) {
	p := &Processor{
		App:      smf,
		consumer: consumer,
	}
	return p, nil
}

func (p *Processor) Consumer() *consumer.Consumer {
	return p.consumer
}
