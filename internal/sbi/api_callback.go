package sbi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/sbi/processor"
	"github.com/free5gc/util/httpwrapper"
)

func (s *Server) getCallbackRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Pattern: "/sm-policies/:smContextRef/update",
			APIFunc: s.HTTPSmPolicyUpdateNotification,
		},
		{
			Method:  http.MethodPost,
			Pattern: "/sm-policies/:smContextRef/terminate",
			APIFunc: s.SmPolicyControlTerminationRequestNotification,
		},
		{
			Method:  http.MethodPost,
			Pattern: "/:notifyUri",
			APIFunc: s.HTTPChargingNotification,
		},
	}
}

// SubscriptionsPost -
func (s *Server) HTTPSmPolicyUpdateNotification(c *gin.Context) {
	var request models.SmPolicyNotification

	reqBody, err := c.GetRawData()
	if err != nil {
		logger.PduSessLog.Errorln("GetRawData failed")
	}

	err = openapi.Deserialize(&request, reqBody, c.ContentType())
	if err != nil {
		logger.PduSessLog.Errorln("Deserialize request failed")
	}

	reqWrapper := httpwrapper.NewRequest(c.Request, request)
	reqWrapper.Params["smContextRef"] = c.Params.ByName("smContextRef")

	smContextRef := reqWrapper.Params["smContextRef"]
	HTTPResponse := processor.HandleSMPolicyUpdateNotify(smContextRef, reqWrapper.Body.(models.SmPolicyNotification))

	for key, val := range HTTPResponse.Header {
		c.Header(key, val[0])
	}

	resBody, err := openapi.Serialize(HTTPResponse.Body, "application/json")
	if err != nil {
		logger.PduSessLog.Errorln("Serialize failed")
	}

	_, err = c.Writer.Write(resBody)
	if err != nil {
		logger.PduSessLog.Errorln("Write failed")
	}
	c.Status(HTTPResponse.Status)
}

func (s *Server) SmPolicyControlTerminationRequestNotification(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

func (s *Server) HTTPChargingNotification(c *gin.Context) {
	var req models.ChargingNotifyRequest

	requestBody, err := c.GetRawData()
	if err != nil {
		logger.PduSessLog.Errorln("GetRawData failed")
	}

	err = openapi.Deserialize(&req, requestBody, "application/json")
	if err != nil {
		logger.PduSessLog.Errorln("Deserialize request failed")
	}

	reqWrapper := httpwrapper.NewRequest(c.Request, req)
	reqWrapper.Params["notifyUri"] = c.Params.ByName("notifyUri")
	smContextRef := strings.Split(reqWrapper.Params["notifyUri"], "_")[1]

	HTTPResponse := s.processor.HandleChargingNotification(reqWrapper.Body.(models.ChargingNotifyRequest), smContextRef)

	for key, val := range HTTPResponse.Header {
		c.Header(key, val[0])
	}

	resBody, err := openapi.Serialize(HTTPResponse.Body, "application/json")
	if err != nil {
		logger.PduSessLog.Errorln("Serialize failed")
	}

	_, err = c.Writer.Write(resBody)
	if err != nil {
		logger.PduSessLog.Errorln("Write failed")
	}
	c.Status(HTTPResponse.Status)
}
