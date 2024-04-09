package oam

import (
	"github.com/gin-gonic/gin"

	"github.com/free5gc/smf/internal/sbi/producer"
)

func HTTPGetSMFUserPlaneInfo(c *gin.Context) {
	HTTPResponse := producer.HandleGetSMFUserPlaneInfo()

	c.JSON(HTTPResponse.Status, HTTPResponse.Body)
}
