package consumer

import (
	"context"
	"net/http"
	"sync"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nsmf_PDUSession"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
)

type nsmfService struct {
	consumer *Consumer

	PDUSessionMu sync.RWMutex

	PDUSessionClients map[string]*Nsmf_PDUSession.APIClient
}

func (s *nsmfService) getPDUSessionClient(uri string) *Nsmf_PDUSession.APIClient {
	if uri == "" {
		return nil
	}
	s.PDUSessionMu.RLock()
	client, ok := s.PDUSessionClients[uri]
	if ok {
		s.PDUSessionMu.RUnlock()
		return client
	}

	configuration := Nsmf_PDUSession.NewConfiguration()
	configuration.SetBasePath(uri)
	client = Nsmf_PDUSession.NewAPIClient(configuration)

	s.PDUSessionMu.RUnlock()
	s.PDUSessionMu.Lock()
	defer s.PDUSessionMu.Unlock()
	s.PDUSessionClients[uri] = client
	return client
}

func (s *nsmfService) SendSMContextStatusNotification(uri string) (*models.ProblemDetails, error) {
	if uri != "" {
		request := models.SmContextStatusNotification{}
		request.StatusInfo = &models.StatusInfo{
			ResourceStatus: models.ResourceStatus_RELEASED,
		}

		client := s.getPDUSessionClient(uri)

		logger.CtxLog.Infoln("[SMF] Send SMContext Status Notification")
		httpResp, localErr := client.
			IndividualSMContextNotificationApi.
			SMContextNotification(context.Background(), uri, request)

		if localErr == nil {
			if httpResp.StatusCode != http.StatusNoContent {
				return nil, openapi.ReportError("Send SMContextStatus Notification Failed")
			}

			logger.PduSessLog.Tracef("Send SMContextStatus Notification Success")
		} else if httpResp != nil {
			defer func() {
				if resCloseErr := httpResp.Body.Close(); resCloseErr != nil {
					logger.ConsumerLog.Errorf("SMContextNotification response body cannot close: %+v", resCloseErr)
				}
			}()
			logger.PduSessLog.Warnf("Send SMContextStatus Notification Error[%s]", httpResp.Status)
			if httpResp.Status != localErr.Error() {
				return nil, localErr
			}
			problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
			return &problem, nil
		} else {
			logger.PduSessLog.Warnln("Http Response is nil in comsumer API SMContextNotification")
			return nil, openapi.ReportError("Send SMContextStatus Notification Failed[%s]", localErr.Error())
		}
	}
	return nil, nil
}
