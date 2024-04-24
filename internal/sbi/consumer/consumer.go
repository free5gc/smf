package consumer

import (
	"context"

	"github.com/free5gc/openapi/Nchf_ConvergedCharging"
	"github.com/free5gc/openapi/Nnrf_NFDiscovery"
	"github.com/free5gc/openapi/Nnrf_NFManagement"
	"github.com/free5gc/openapi/Npcf_SMPolicyControl"
	"github.com/free5gc/openapi/Nsmf_PDUSession"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/Nudm_UEContextManagement"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/factory"
)

type smf interface {
	Config() *factory.Config
	Context() *smf_context.SMFContext
	CancelContext() context.Context
}

type Consumer struct {
	smf

	// consumer services
	*nsmfService
	*nchfService // Not sure
	*npcfService // Not sure
	*nudmService
	*nnrfService
}

func NewConsumer(smf smf) (*Consumer, error) {
	c := &Consumer{
		smf: smf,
	}

	c.nsmfService = &nsmfService{
		consumer:          c,
		PDUSessionClients: make(map[string]*Nsmf_PDUSession.APIClient),
	}

	c.nchfService = &nchfService{
		consumer:                 c,
		ConvergedChargingClients: make(map[string]*Nchf_ConvergedCharging.APIClient),
	}

	c.nudmService = &nudmService{
		consumer:                        c,
		SubscriberDataManagementClients: make(map[string]*Nudm_SubscriberDataManagement.APIClient),
		UEContextManagementClients:      make(map[string]*Nudm_UEContextManagement.APIClient),
	}

	c.nnrfService = &nnrfService{
		consumer:            c,
		NFManagementClients: make(map[string]*Nnrf_NFManagement.APIClient),
		NFDiscoveryClients:  make(map[string]*Nnrf_NFDiscovery.APIClient),
	}

	c.npcfService = &npcfService{
		consumer:               c,
		SMPolicyControlClients: make(map[string]*Npcf_SMPolicyControl.APIClient),
	}

	return c, nil
}
