package consumer

import (
	"github.com/free5gc/openapi/amf/Comm"
	"github.com/free5gc/openapi/chf/ConvCharging"
	"github.com/free5gc/openapi/nrf/NFDisc"
	"github.com/free5gc/openapi/nrf/NFMgmt"
	"github.com/free5gc/openapi/pcf/SMPolCtrl"
	"github.com/free5gc/openapi/smf/PDUSess"
	"github.com/free5gc/openapi/udm/SDM"
	"github.com/free5gc/openapi/udm/UECM"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/app"
	"github.com/free5gc/util/nfheartbeat"
)

type Consumer struct {
	app.App

	// consumer services
	*nsmfService
	*namfService
	*nchfService
	*npcfService
	*nudmService
	*nnrfService
	*nbsfService // BSF service for PCF binding discovery
}

func NewConsumer(smf app.App) (*Consumer, error) {
	c := &Consumer{
		App: smf,
	}

	c.nsmfService = &nsmfService{
		consumer:          c,
		PDUSessionClients: make(map[string]*PDUSess.APIClient),
	}

	c.namfService = &namfService{
		consumer:             c,
		CommunicationClients: make(map[string]*Comm.APIClient),
	}

	c.nchfService = &nchfService{
		consumer:                 c,
		ConvergedChargingClients: make(map[string]*ConvCharging.APIClient),
	}

	c.nudmService = &nudmService{
		consumer:                        c,
		SubscriberDataManagementClients: make(map[string]*SDM.APIClient),
		UEContextManagementClients:      make(map[string]*UECM.APIClient),
	}

	c.nnrfService = &nnrfService{
		consumer:            c,
		NFManagementClients: make(map[string]*NFMgmt.APIClient),
		NFDiscoveryClients:  make(map[string]*NFDisc.APIClient),
	}
	heartbeat, err := nfheartbeat.NewRunner(
		nrfRegistrar{c.nnrfService},
		func() int32 { return c.Config().GetNfHeartBeatTimer() },
		logger.ConsumerLog,
	)
	if err != nil {
		return nil, err
	}
	c.heartbeat = heartbeat

	c.npcfService = &npcfService{
		consumer:               c,
		SMPolicyControlClients: make(map[string]*SMPolCtrl.APIClient),
	}

	c.nbsfService = &nbsfService{
		consumer: c,
	}

	return c, nil
}

// BSFAwarePCFSelection performs PCF selection with BSF binding awareness
func (c *Consumer) BSFAwarePCFSelection(smContext *smf_context.SMContext) error {
	return c.nbsfService.PCFSelectionWithBSF(smContext)
}

// NotifyBSFBindingRelease notifies BSF about PCF binding release
func (c *Consumer) NotifyBSFBindingRelease(smContext *smf_context.SMContext) {
	c.nbsfService.NotifyPCFBindingRelease(smContext)
}
