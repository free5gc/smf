package sbi

import "github.com/gin-gonic/gin"

type Route struct {
	Name    string
	Method  string
	Pattern string
	APIFunc gin.HandlerFunc
	// IsNotification identifies a Notify operation of Subscribe/Notify semantics.
	// TS 29.500 6.7.3 excludes these operations from OAuth access-token use.
	IsNotification bool
}

func applyRoutes(group *gin.RouterGroup, routes []Route) {
	for _, route := range routes {
		switch route.Method {
		case "GET":
			group.GET(route.Pattern, route.APIFunc)
		case "POST":
			group.POST(route.Pattern, route.APIFunc)
		case "PUT":
			group.PUT(route.Pattern, route.APIFunc)
		case "PATCH":
			group.PATCH(route.Pattern, route.APIFunc)
		case "DELETE":
			group.DELETE(route.Pattern, route.APIFunc)
		}
	}
}
