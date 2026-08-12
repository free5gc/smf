package consumer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	NupfEventExposure "github.com/free5gc/openapi/upf/EventExposure"
	smf_context "github.com/free5gc/smf/internal/context"
	sbi_metrics "github.com/free5gc/util/metrics/sbi"
)

type NupfEventExposureErrorKind string

const (
	NupfEventExposureErrorToken            NupfEventExposureErrorKind = "token"
	NupfEventExposureErrorRedirect         NupfEventExposureErrorKind = "redirect"
	NupfEventExposureErrorTransport        NupfEventExposureErrorKind = "transport"
	NupfEventExposureErrorUpstreamProblem  NupfEventExposureErrorKind = "upstream_problem"
	NupfEventExposureErrorMalformedSuccess NupfEventExposureErrorKind = "malformed_success"
)

type NupfEventExposureError struct {
	Kind           NupfEventExposureErrorKind
	StatusCode     int
	ProblemDetails *models.ProblemDetails
	Operation      string
}

func (e *NupfEventExposureError) Error() string {
	return fmt.Sprintf("nupf event exposure %s failed: %s", e.Operation, e.Kind)
}

type nupfEventExposureService struct {
	consumer *Consumer

	EventExposureMu             sync.RWMutex
	EventExposureClients        map[string]*NupfEventExposure.APIClient
	EventExposureCreateRequests map[string]string
}

func (s *nupfEventExposureService) getEventExposureClient(apiRoot string) *NupfEventExposure.APIClient {
	if apiRoot == "" {
		return nil
	}

	s.EventExposureMu.RLock()
	client, ok := s.EventExposureClients[apiRoot]
	if ok {
		s.EventExposureMu.RUnlock()
		return client
	}

	configuration := NupfEventExposure.NewConfiguration()
	configuration.SetBasePath(apiRoot)
	configuration.SetMetrics(sbi_metrics.SbiMetricHook)
	configuration.SetRedirectPolicy(openapi.RejectRedirects)
	client = NupfEventExposure.NewAPIClient(configuration)
	requestURI := strings.TrimRight(configuration.BasePath(), "/") + "/ee-subscriptions"

	s.EventExposureMu.RUnlock()
	s.EventExposureMu.Lock()
	defer s.EventExposureMu.Unlock()
	if existing, exists := s.EventExposureClients[apiRoot]; exists {
		return existing
	}
	s.EventExposureClients[apiRoot] = client
	s.EventExposureCreateRequests[apiRoot] = requestURI
	return client
}

func (s *nupfEventExposureService) CreateSubscription(
	ctx context.Context,
	target smf_context.EventExposureTarget,
	request models.UpfCreateEventSubscription,
) (smf_context.NupfCreateResult, error) {
	client := s.getEventExposureClient(target.APIroot)
	if client == nil {
		return smf_context.NupfCreateResult{}, &NupfEventExposureError{
			Kind:      NupfEventExposureErrorTransport,
			Operation: "create",
		}
	}

	callCtx, err := s.tokenContext(ctx)
	if err != nil {
		return smf_context.NupfCreateResult{}, &NupfEventExposureError{
			Kind:      NupfEventExposureErrorToken,
			Operation: "create",
		}
	}

	createRequest := &NupfEventExposure.CreateSubscriptionRequest{}
	createRequest.SetUpfCreateEventSubscription(request)
	response, err := client.SubscriptionsCollectionApi.CreateSubscription(callCtx, createRequest)
	if err != nil {
		return smf_context.NupfCreateResult{}, classifyNupfCreateError(err)
	}
	result, err := validateNupfCreateSuccess(target, s.createRequestURI(target.APIroot), response)
	if err != nil {
		return smf_context.NupfCreateResult{}, &NupfEventExposureError{
			Kind:      NupfEventExposureErrorMalformedSuccess,
			Operation: "create",
		}
	}
	return result, nil
}

func (s *nupfEventExposureService) DeleteSubscription(
	ctx context.Context,
	target smf_context.EventExposureTarget,
	subscriptionID string,
) error {
	client := s.getEventExposureClient(target.APIroot)
	if client == nil {
		return &NupfEventExposureError{Kind: NupfEventExposureErrorTransport, Operation: "delete"}
	}

	callCtx, err := s.tokenContext(ctx)
	if err != nil {
		return &NupfEventExposureError{Kind: NupfEventExposureErrorToken, Operation: "delete"}
	}

	deleteRequest := &NupfEventExposure.DeleteSubscriptionRequest{}
	deleteRequest.SetSubscriptionId(subscriptionID)
	_, err = client.IndividualSubscriptionDocumentApi.DeleteSubscription(callCtx, deleteRequest)
	if err != nil {
		return classifyNupfDeleteError(err)
	}
	return nil
}

func (s *nupfEventExposureService) tokenContext(ctx context.Context) (context.Context, error) {
	if !s.consumer.Context().OAuth2Required {
		return ctx, nil
	}

	tokenCtx, _, err := s.consumer.Context().GetTokenCtx(
		models.ServiceName_NUPF_EE, models.NrfNfManagementNfType_UPF)
	if err != nil {
		return nil, err
	}
	return tokenCtx, nil
}

func (s *nupfEventExposureService) createRequestURI(apiRoot string) string {
	s.EventExposureMu.RLock()
	defer s.EventExposureMu.RUnlock()
	return s.EventExposureCreateRequests[apiRoot]
}

func classifyNupfCreateError(err error) error {
	return classifyNupfError("create", err)
}

func classifyNupfDeleteError(err error) error {
	return classifyNupfError("delete", err)
}

func classifyNupfError(operation string, err error) error {
	var apiError openapi.GenericOpenAPIError
	if errors.As(err, &apiError) {
		classified := &NupfEventExposureError{
			StatusCode: apiError.ErrorStatus,
			Operation:  operation,
		}
		switch model := apiError.Model().(type) {
		case NupfEventExposure.CreateSubscriptionError:
			classified.Kind = nupfErrorKindFromCreateStatus(apiError.ErrorStatus)
			if model.ProblemDetails.Status != 0 {
				classified.ProblemDetails = &model.ProblemDetails
			}
		case NupfEventExposure.DeleteSubscriptionError:
			classified.Kind = nupfErrorKindFromDeleteStatus(apiError.ErrorStatus)
			if model.ProblemDetails.Status != 0 {
				classified.ProblemDetails = &model.ProblemDetails
			}
		default:
			classified.Kind = NupfEventExposureErrorTransport
		}
		return classified
	}
	return &NupfEventExposureError{Kind: NupfEventExposureErrorTransport, Operation: operation}
}

func nupfErrorKindFromCreateStatus(status int) NupfEventExposureErrorKind {
	if status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect {
		return NupfEventExposureErrorRedirect
	}
	return NupfEventExposureErrorUpstreamProblem
}

func nupfErrorKindFromDeleteStatus(status int) NupfEventExposureErrorKind {
	if status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect {
		return NupfEventExposureErrorRedirect
	}
	return NupfEventExposureErrorUpstreamProblem
}

func validateNupfCreateSuccess(
	target smf_context.EventExposureTarget,
	requestURI string,
	response *NupfEventExposure.CreateSubscriptionResponse,
) (smf_context.NupfCreateResult, error) {
	if response == nil {
		return smf_context.NupfCreateResult{}, errors.New("missing response")
	}
	subscriptionID := response.UpfCreatedEventSubscription.SubscriptionId
	if subscriptionID == "" || response.Location == "" {
		return smf_context.NupfCreateResult{}, errors.New("missing subscription linkage")
	}

	resolvedLocation, err := resolveNupfLocation(requestURI, response.Location)
	if err != nil {
		return smf_context.NupfCreateResult{}, err
	}
	if locationErr := validateNupfLocation(target, resolvedLocation, subscriptionID); locationErr != nil {
		return smf_context.NupfCreateResult{}, locationErr
	}

	return smf_context.NupfCreateResult{
		SubscriptionID:    subscriptionID,
		ValidatedLocation: resolvedLocation.String(),
		CreateRequestURI:  requestURI,
		Response:          response.UpfCreatedEventSubscription,
		StatusCode:        http.StatusCreated,
	}, nil
}

func resolveNupfLocation(requestURI, location string) (*url.URL, error) {
	base, err := url.Parse(requestURI)
	if err != nil {
		return nil, err
	}
	ref, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if hasNupfDotPathSegment(ref.Path) {
		return nil, errors.New("invalid location path")
	}
	resolved := base.ResolveReference(ref)
	if resolved.User != nil || resolved.RawQuery != "" || resolved.Fragment != "" {
		return nil, errors.New("invalid location metadata")
	}
	return resolved, nil
}

func validateNupfLocation(
	target smf_context.EventExposureTarget,
	location *url.URL,
	subscriptionID string,
) error {
	expectedBase, err := url.Parse(target.ServiceBaseURL)
	if err != nil {
		return err
	}
	if location.Scheme != expectedBase.Scheme || location.Host != expectedBase.Host {
		return errors.New("location origin mismatch")
	}

	if !validNupfSubscriptionIDSegment(subscriptionID) {
		return errors.New("invalid subscription id")
	}

	expectedPath := strings.TrimRight(expectedBase.EscapedPath(), "/") +
		"/ee-subscriptions/" + subscriptionID
	if location.EscapedPath() != expectedPath {
		return errors.New("location path mismatch")
	}
	return nil
}

func validNupfSubscriptionIDSegment(subscriptionID string) bool {
	return subscriptionID != "" &&
		subscriptionID != "." &&
		subscriptionID != ".." &&
		!strings.Contains(subscriptionID, "/") &&
		!strings.Contains(subscriptionID, `\`) &&
		url.PathEscape(subscriptionID) == subscriptionID
}

func hasNupfDotPathSegment(locationPath string) bool {
	for _, segment := range strings.Split(locationPath, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
