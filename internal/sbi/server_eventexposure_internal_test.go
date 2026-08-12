package sbi

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/free5gc/smf/pkg/factory"
)

func TestEventExposureRouteRegistrationFollowsServiceNameList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		services  []string
		wantRoute bool
	}{
		{
			name:      "present registers route",
			services:  []string{"nsmf-pdusession", "nsmf-event-exposure"},
			wantRoute: true,
		},
		{
			name:      "absent leaves route unregistered",
			services:  []string{"nsmf-pdusession"},
			wantRoute: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldConfig := factory.SmfConfig
			factory.SmfConfig = &factory.Config{
				Configuration: &factory.Configuration{
					ServiceNameList: tt.services,
				},
			}
			t.Cleanup(func() {
				factory.SmfConfig = oldConfig
			})

			router := newRouter(&Server{ServerSmf: fakeServerSMF{}})
			found := hasRoute(
				router.Routes(),
				http.MethodPost,
				factory.SmfEventExposureResUriPrefix+"/subscriptions",
			)
			if found != tt.wantRoute {
				t.Fatalf("route presence mismatch: got %v want %v", found, tt.wantRoute)
			}
		})
	}
}

func hasRoute(routes gin.RoutesInfo, method, path string) bool {
	for _, route := range routes {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}
