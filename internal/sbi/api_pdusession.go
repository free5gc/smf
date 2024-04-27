package sbi

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/sbi/producer"
	"github.com/free5gc/util/httpwrapper"
)

func (s *Server) getPDUSessionRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Pattern: "/",
			APIFunc: func(ctx *gin.Context) {
				ctx.JSON(http.StatusOK, gin.H{"status": "Service Available"})
			},
		},
		{
			Method:  http.MethodPost,
			Pattern: "/pdu-sessions/:pduSessionRef/release",
			APIFunc: s.ReleasePduSession,
		},
		{
			Method:  http.MethodPost,
			Pattern: "/pdu-sessions/:pduSessionRef/modify",
			APIFunc: s.UpdatePduSession,
		},
		{
			Method:  http.MethodPost,
			Pattern: "/sm-contexts/:smContextRef/release",
			APIFunc: s.HTTPReleaseSmContext,
		},
		{
			Method:  http.MethodPost,
			Pattern: "/sm-contexts/:smContextRef/retrieve",
			APIFunc: s.RetrieveSmContext,
		},
		{
			Method:  http.MethodPost,
			Pattern: "/sm-contexts/:smContextRef/modify",
			APIFunc: s.HTTPUpdateSmContext,
		},
		{
			Method:  http.MethodPatch,
			Pattern: "/pdu-sessions",
			APIFunc: s.PostPduSessions,
		},
		{
			Method:  http.MethodPost,
			Pattern: "/sm-contexts",
			APIFunc: s.HTTPPostSmContexts,
		},
	}
}

// ReleasePduSession - Release
func (s *Server) ReleasePduSession(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// UpdatePduSession - Update (initiated by V-SMF)
func (s *Server) UpdatePduSession(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// HTTPReleaseSmContext - Release SM Context
func (s *Server) HTTPReleaseSmContext(c *gin.Context) {
	logger.PduSessLog.Info("Receive Release SM Context Request")
	var request models.ReleaseSmContextRequest
	request.JsonData = new(models.SmContextReleaseData)

	contentType := strings.Split(c.GetHeader("Content-Type"), ";")
	var err error
	switch contentType[0] {
	case "application/json":
		err = c.ShouldBindJSON(request.JsonData)
	case "multipart/related":
		err = c.ShouldBindWith(&request, openapi.MultipartRelatedBinding{})
	}
	if err != nil {
		log.Print(err)
		return
	}

	req := httpwrapper.NewRequest(c.Request, request)
	req.Params["smContextRef"] = c.Params.ByName("smContextRef")

	smContextRef := req.Params["smContextRef"]
	producer.HandlePDUSessionSMContextRelease(
		smContextRef, req.Body.(models.ReleaseSmContextRequest))

	c.Status(http.StatusNoContent)
}

// RetrieveSmContext - Retrieve SM Context
func (s *Server) RetrieveSmContext(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// HTTPUpdateSmContext - Update SM Context
func (s *Server) HTTPUpdateSmContext(c *gin.Context) {
	logger.PduSessLog.Info("Receive Update SM Context Request")
	var request models.UpdateSmContextRequest
	request.JsonData = new(models.SmContextUpdateData)

	contentType := strings.Split(c.GetHeader("Content-Type"), ";")
	var err error
	switch contentType[0] {
	case "application/json":
		err = c.ShouldBindJSON(request.JsonData)
	case "multipart/related":
		err = c.ShouldBindWith(&request, openapi.MultipartRelatedBinding{})
	}
	if err != nil {
		log.Print(err)
		return
	}

	req := httpwrapper.NewRequest(c.Request, request)
	req.Params["smContextRef"] = c.Params.ByName("smContextRef")

	smContextRef := req.Params["smContextRef"]
	HTTPResponse := producer.HandlePDUSessionSMContextUpdate(
		smContextRef, req.Body.(models.UpdateSmContextRequest))

	if HTTPResponse.Status < 300 {
		c.Render(HTTPResponse.Status, openapi.MultipartRelatedRender{Data: HTTPResponse.Body})
	} else {
		c.JSON(HTTPResponse.Status, HTTPResponse.Body)
	}
}

// PostPduSessions - Create
func (s *Server) PostPduSessions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// HTTPPostSmContexts - Create SM Context
func (s *Server) HTTPPostSmContexts(c *gin.Context) {
	logger.PduSessLog.Info("Receive Create SM Context Request")
	var request models.PostSmContextsRequest

	request.JsonData = new(models.SmContextCreateData)

	contentType := strings.Split(c.GetHeader("Content-Type"), ";")
	var err error
	switch contentType[0] {
	case "application/json":
		err = c.ShouldBindJSON(request.JsonData)
	case "multipart/related":
		err = c.ShouldBindWith(&request, openapi.MultipartRelatedBinding{})
	}

	if err != nil {
		problemDetail := "[Request Body] " + err.Error()
		rsp := models.ProblemDetails{
			Title:  "Malformed request syntax",
			Status: http.StatusBadRequest,
			Detail: problemDetail,
		}
		logger.PduSessLog.Errorln(problemDetail)
		c.JSON(http.StatusBadRequest, rsp)
		return
	}

	req := httpwrapper.NewRequest(c.Request, request)
	isDone := c.Done()
	HTTPResponse := producer.HandlePDUSessionSMContextCreate(isDone,
		req.Body.(models.PostSmContextsRequest))
	// Http Response to AMF
	for key, val := range HTTPResponse.Header {
		c.Header(key, val[0])
	}
	switch HTTPResponse.Status {
	case http.StatusCreated,
		http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		c.Render(HTTPResponse.Status, openapi.MultipartRelatedRender{Data: HTTPResponse.Body})
	default:
		c.JSON(HTTPResponse.Status, HTTPResponse.Body)
	}
}
