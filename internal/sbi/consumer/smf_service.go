package consumer

import (
	"context"
	"sync"
	"time"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/smf/PDUSess"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	sbi_metrics "github.com/free5gc/util/metrics/sbi"
)

type nsmfService struct {
	consumer *Consumer

	PDUSessionMu sync.RWMutex

	PDUSessionClients map[string]*PDUSess.APIClient
}

func (s *nsmfService) getPDUSessionClient(uri string) *PDUSess.APIClient {
	if uri == "" {
		return nil
	}
	s.PDUSessionMu.RLock()
	client, ok := s.PDUSessionClients[uri]
	if ok {
		s.PDUSessionMu.RUnlock()
		return client
	}

	configuration := PDUSess.NewConfiguration()
	configuration.SetBasePath(uri)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	client = PDUSess.NewAPIClient(configuration)

	s.PDUSessionMu.RUnlock()
	s.PDUSessionMu.Lock()
	defer s.PDUSessionMu.Unlock()
	s.PDUSessionClients[uri] = client
	return client
}

func (s *nsmfService) SendSMContextStatusNotification(uri string) (*models.ProblemDetails, error) {
	if uri != "" {
		request := &PDUSess.PostSmContextsSmContextStatusNotificationRequest{
			RequestBody: &models.Smf_PDUSess_SmContextStatusNotification{
				StatusInfo: &models.Smf_PDUSess_StatusInfo{
					ResourceStatus: models.Smf_PDUSess_ResourceStatus_RELEASED,
				},
			},
		}

		client := s.getPDUSessionClient(uri)

		ctx, pd, err := smf_context.GetSelf().GetTokenCtx(
			models.Nrf_NFMgmt_ServiceName("namf-callback"), models.Nrf_NFMgmt_NFType_AMF)
		if err != nil {
			logger.CtxLog.Warnf("[SMF] Get token for AMF callback failed: %+v", pd)
			return pd, err
		}

		// An unreachable callback must not hold local resource removal indefinitely.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		logger.CtxLog.Infoln("[SMF] Send SMContext Status Notification")
		_, localErr := client.SMContextsCollectionApi.
			PostSmContextsSmContextStatusNotification(ctx, uri, request)

		switch err := localErr.(type) {
		case openapi.GenericOpenAPIError:
			switch errModel := err.Model().(type) {
			case PDUSess.PostSmContextsSmContextStatusNotificationError:
				return errModel.ProblemDetails, nil
			case error:
				return openapi.ProblemDetailsSystemFailure(errModel.Error()), nil
			default:
				return nil, openapi.ReportError("openapi error")
			}
		case error:
			return openapi.ProblemDetailsSystemFailure(err.Error()), nil
		case nil:
			logger.PduSessLog.Tracef("Send SMContextStatus Notification Success")
			return nil, nil
		default:
			logger.PduSessLog.Warnf("Send SMContextStatus Notification Unknown Error: %+v", err)
			return nil, openapi.ReportError("server no response")
		}
	}
	return nil, nil
}
