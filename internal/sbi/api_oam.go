package sbi

import (
	"net/http"
	"github.com/gin-gonic/gin"

	"github.com/free5gc/smf/internal/sbi/producer"
	"github.com/free5gc/util/httpwrapper"
)

func (s *Server) getOAMRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Pattern: "/",
			APIFunc: func(ctx *gin.Context) {
				ctx.JSON(http.StatusOK, gin.H{"status": "Service Available"})
			},
		},
		{
			Method:  http.MethodGet,
			Pattern: "/ue-pdu-session-info/:smContextRef",
			APIFunc: s.HTTPGetUEPDUSessionInfo,
		},
		{
			Method:  http.MethodGet,
			Pattern: "/user-plane-info/",
			APIFunc: s.HTTPGetSMFUserPlaneInfo,
		},
	}
}

func (s *Server) HTTPGetUEPDUSessionInfo(c *gin.Context) {
	req := httpwrapper.NewRequest(c.Request, nil)
	req.Params["smContextRef"] = c.Params.ByName("smContextRef")

	smContextRef := req.Params["smContextRef"]
	HTTPResponse := producer.HandleOAMGetUEPDUSessionInfo(smContextRef)

	c.JSON(HTTPResponse.Status, HTTPResponse.Body)
}

func (s *Server) HTTPGetSMFUserPlaneInfo(c *gin.Context) {
	HTTPResponse := producer.HandleGetSMFUserPlaneInfo()

	c.JSON(HTTPResponse.Status, HTTPResponse.Body)
}
