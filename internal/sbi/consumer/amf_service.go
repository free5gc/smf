package consumer

import (
	"context"
	"fmt"
	"sync"

	"github.com/free5gc/openapi/Namf_Communication"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
)

type namfService struct {
	consumer *Consumer

	CommunicationMu sync.RWMutex

	CommunicationClients map[string]*Namf_Communication.APIClient
}

func (s *namfService) getCommunicationClient(uri string) *Namf_Communication.APIClient {
	if uri == "" {
		return nil
	}
	s.CommunicationMu.RLock()
	client, ok := s.CommunicationClients[uri]
	if ok {
		s.CommunicationMu.RUnlock()
		return client
	}

	configuration := Namf_Communication.NewConfiguration()
	configuration.SetBasePath(uri)
	client = Namf_Communication.NewAPIClient(configuration)

	s.CommunicationMu.RUnlock()
	s.CommunicationMu.Lock()
	defer s.CommunicationMu.Unlock()
	s.CommunicationClients[uri] = client
	return client
}

func (s *namfService) N1N2MessageTransfer(
	ctx context.Context, supi string, n1n2Request models.N1N2MessageTransferRequest, apiPrefix string,
) (*models.N1N2MessageTransferRspData, *int, error) {
	client := s.getCommunicationClient(apiPrefix)
	if client == nil {
		return nil, nil, fmt.Errorf("N1N2MessageTransfer client is nil: (%v)", apiPrefix)
	}

	rspData, rsp, err := client.N1N2MessageCollectionDocumentApi.N1N2MessageTransfer(ctx, supi, n1n2Request)
	if err != nil {
		return nil, nil, err
	}

	defer func() {
		if errClose := rsp.Body.Close(); errClose != nil {
			logger.ConsumerLog.Warnf("Close body failed %v", errClose)
		}
	}()

	statusCode := rsp.StatusCode
	return &rspData, &statusCode, err
}
