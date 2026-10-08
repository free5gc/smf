package consumer

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/upf/EvtExpos"
	sbi_metrics "github.com/free5gc/util/metrics/sbi"
)

const nupfEventExposureFailure = "nupf event exposure operation failed"

type nupfEventExposureService struct {
	consumer *Consumer

	mu      sync.RWMutex
	clients map[string]*EvtExpos.APIClient
}

// NupfEventExposureError contains only the downstream classification needed by
// the processor. It deliberately retains no response content or endpoint data.
type NupfEventExposureError struct {
	StatusCode int
	TimedOut   bool
}

func (e *NupfEventExposureError) Error() string {
	return nupfEventExposureFailure
}

func (s *nupfEventExposureService) getClient(apiRoot string) *EvtExpos.APIClient {
	if apiRoot == "" {
		return nil
	}

	s.mu.RLock()
	client, ok := s.clients[apiRoot]
	if ok {
		s.mu.RUnlock()
		return client
	}

	configuration := EvtExpos.NewConfiguration()
	configuration.SetBasePath(apiRoot)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	configuration.SetRedirectPolicy(openapi.RejectRedirects)
	client = EvtExpos.NewAPIClient(configuration)
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, loaded := s.clients[apiRoot]; loaded {
		return existing
	}
	s.clients[apiRoot] = client
	return client
}

// CreateNupfEventSubscription sends one generated Nupf Create request and
// returns only the validated downstream subscription ID.
func (s *nupfEventExposureService) CreateNupfEventSubscription(
	ctx context.Context,
	apiRoot string,
	subscription *models.Upf_EvtExpos_CreateEventSubscription,
) (string, error) {
	client := s.getClient(apiRoot)
	if client == nil {
		return "", &NupfEventExposureError{}
	}

	requestContext, err := s.tokenContext(ctx)
	if err != nil {
		return "", classifyNupfEventExposureError(err)
	}

	request := &EvtExpos.CreateSubscriptionRequest{RequestBody: subscription}
	response, err := client.SubscriptionsCollectionApi.CreateSubscription(requestContext, request)
	if err != nil {
		return "", classifyNupfEventExposureError(err)
	}

	downstreamID, err := validateNupfCreateResponse(apiRoot, response)
	if err != nil {
		return "", &NupfEventExposureError{}
	}
	return downstreamID, nil
}

// DeleteNupfEventSubscription sends one generated Nupf Delete request.
func (s *nupfEventExposureService) DeleteNupfEventSubscription(
	ctx context.Context,
	apiRoot string,
	downstreamID string,
) error {
	client := s.getClient(apiRoot)
	if client == nil {
		return &NupfEventExposureError{}
	}

	requestContext, err := s.tokenContext(ctx)
	if err != nil {
		return classifyNupfEventExposureError(err)
	}

	request := &EvtExpos.DeleteSubscriptionRequest{SubscriptionId: &downstreamID}
	response, err := client.DefaultApi.DeleteSubscription(requestContext, request)
	if err != nil {
		return classifyNupfEventExposureError(err)
	}
	if response == nil {
		return &NupfEventExposureError{}
	}
	return nil
}

func (s *nupfEventExposureService) tokenContext(ctx context.Context) (context.Context, error) {
	smfContext := s.consumer.Context()
	tokenContext, _, err := smfContext.GetTokenCtx(
		models.Nrf_NFMgmt_ServiceName_NUPF_EE,
		models.Nrf_NFMgmt_NFType_UPF,
	)
	if err != nil {
		return nil, err
	}
	if !smfContext.OAuth2Required {
		return ctx, nil
	}
	return tokenContext, nil
}

func validateNupfCreateResponse(apiRoot string, response *EvtExpos.CreateSubscriptionResponse) (string, error) {
	if response == nil || response.Upf_EvtExpos_CreatedEventSubscription == nil {
		return "", errors.New(nupfEventExposureFailure)
	}

	created := response.Upf_EvtExpos_CreatedEventSubscription
	if created.Subscription == nil || created.SubscriptionId == "" || response.Location == "" ||
		created.SubscriptionId != response.Location {
		return "", errors.New(nupfEventExposureFailure)
	}

	return validateNupfCreatedResource(apiRoot, created.SubscriptionId)
}

func validateNupfCreatedResource(apiRoot, resource string) (string, error) {
	baseURL, err := url.Parse(apiRoot)
	if err != nil {
		return "", errors.New(nupfEventExposureFailure)
	}
	resourceURL, err := url.Parse(resource)
	if err != nil || resourceURL.String() != resource ||
		(resourceURL.Scheme != "http" && resourceURL.Scheme != "https") ||
		resourceURL.Hostname() == "" || resourceURL.User != nil || resourceURL.RawQuery != "" ||
		resourceURL.ForceQuery || resourceURL.Fragment != "" {
		return "", errors.New(nupfEventExposureFailure)
	}
	if resourceURL.Scheme != baseURL.Scheme || resourceURL.Host != baseURL.Host {
		return "", errors.New(nupfEventExposureFailure)
	}

	expectedPathPrefix := strings.TrimSuffix(baseURL.EscapedPath(), "/") + "/nupf-ee/v1/ee-subscriptions/"
	resourcePath := resourceURL.EscapedPath()
	if !strings.HasPrefix(resourcePath, expectedPathPrefix) {
		return "", errors.New(nupfEventExposureFailure)
	}
	downstreamID := strings.TrimPrefix(resourcePath, expectedPathPrefix)
	if downstreamID == "" || strings.Contains(downstreamID, "/") {
		return "", errors.New(nupfEventExposureFailure)
	}
	return downstreamID, nil
}

func classifyNupfEventExposureError(err error) *NupfEventExposureError {
	classified := &NupfEventExposureError{}
	var apiError openapi.GenericOpenAPIError
	if errors.As(err, &apiError) {
		classified.StatusCode = apiError.ErrorStatus
		return classified
	}
	if errors.Is(err, context.DeadlineExceeded) {
		classified.TimedOut = true
		return classified
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		classified.TimedOut = true
	}
	return classified
}
