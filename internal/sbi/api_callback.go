package sbi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/internal/logger"
)

func (s *Server) getCallbackRoutes() []Route {
	return []Route{
		{
			Name:    "SmPolicyUpdateNotification",
			Method:  http.MethodPost,
			Pattern: "/sm-policies/:smContextRef/update",
			APIFunc: s.HTTPSmPolicyUpdateNotification,
			// TS 29.512 4.2.3.2: Npcf_SMPolicyControl_UpdateNotify.
			IsNotification: true,
		},
		{
			Name:    "SmPolicyControlTerminationRequestNotification",
			Method:  http.MethodPost,
			Pattern: "/sm-policies/:smContextRef/terminate",
			APIFunc: s.SmPolicyControlTerminationRequestNotification,
			// TS 29.512 4.2.3.3: termination is part of UpdateNotify.
			IsNotification: true,
		},
		{
			Name:    "ChargingNotification",
			Method:  http.MethodPost,
			Pattern: "/:notifyUri",
			APIFunc: s.HTTPChargingNotification,
			// TS 32.290 6.2.2/6.2.5: Notify for an implicit charging subscription.
			IsNotification: true,
		},
	}
}

// SubscriptionsPost -
func (s *Server) HTTPSmPolicyUpdateNotification(c *gin.Context) {
	var request models.Pcf_SMPolCtrl_SmPolicyNotification

	reqBody, err := c.GetRawData()
	if err != nil {
		logger.PduSessLog.Errorln("GetRawData failed")
	}

	err = openapi.Deserialize(&request, reqBody, c.ContentType())
	if err != nil {
		logger.PduSessLog.Errorln("Deserialize request failed")
	}

	smContextRef := c.Params.ByName("smContextRef")
	s.Processor().HandleSMPolicyUpdateNotify(c, request, smContextRef)
}

func (s *Server) SmPolicyControlTerminationRequestNotification(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{})
}

func (s *Server) HTTPChargingNotification(c *gin.Context) {
	var req models.Chf_ConvCharging_ChargingNotifyRequest

	requestBody, err := c.GetRawData()
	if err != nil {
		logger.PduSessLog.Errorln("GetRawData failed")
	}

	err = openapi.Deserialize(&req, requestBody, APPLICATION_JSON)
	if err != nil {
		logger.PduSessLog.Errorln("Deserialize request failed")
	}

	notifyURI := c.Params.ByName("notifyUri")
	uriparts := strings.SplitN(notifyURI, "_", 2)
	if len(uriparts) != 2 || uriparts[0] != "notify" || uriparts[1] == "" {
		logger.PduSessLog.Warnf("Invalid notifyUri format: %s", notifyURI)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid notifyUri format"})
		return
	}

	smContextRef := uriparts[1]

	s.Processor().HandleChargingNotification(c, req, smContextRef)
}
