package upi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/pkg/association"
	"github.com/free5gc/smf/pkg/factory"
	"github.com/free5gc/util/httpwrapper"
)

func GetUpNodesLinks(c *gin.Context) {
	upi := smf_context.SMF_Self().UserPlaneInformation
	upi.Mu.RLock()
	defer upi.Mu.RUnlock()

	nodes := upi.UpNodesToConfiguration()
	links := upi.LinksToConfiguration()

	json := &factory.UserPlaneInformation{
		UPNodes: nodes,
		Links:   links,
	}

	httpResponse := &httpwrapper.Response{
		Header: nil,
		Status: http.StatusOK,
		Body:   json,
	}
	c.JSON(httpResponse.Status, httpResponse.Body)
}

func PostUpNodesLinks(c *gin.Context) {
	upi := smf_context.SMF_Self().UserPlaneInformation
	upi.Mu.Lock()
	defer upi.Mu.Unlock()

	var json factory.UserPlaneInformation
	if err := c.ShouldBindJSON(&json); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	upi.UpNodesFromConfiguration(&json)
	upi.LinksFromConfiguration(&json)

	for _, upf := range upi.UPFs {
		// only associate new ones
		if upf.UPF.UPFStatus == smf_context.NotAssociated {
			go association.ToBeAssociatedWithUPF(smf_context.SMF_Self().Ctx, upf.UPF)
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "OK"})
}

func DeleteUpNodeLink(c *gin.Context) {
	// current version does not allow node deletions when ulcl is enabled
	if smf_context.SMF_Self().ULCLSupport {
		c.JSON(http.StatusForbidden, gin.H{})
	} else {
		req := httpwrapper.NewRequest(c.Request, nil)
		req.Params["upfRef"] = c.Params.ByName("upfRef")
		upfRef := req.Params["upfRef"]
		upi := smf_context.SMF_Self().UserPlaneInformation
		upi.Mu.Lock()
		defer upi.Mu.Unlock()
		if upNode, ok := upi.UPNodes[upfRef]; ok {
			if upNode.Type == smf_context.UPNODE_UPF {
				go association.ReleaseAllResourcesOfUPF(upNode.UPF)
			}
			upi.UpNodeDelete(upfRef)
			c.JSON(http.StatusOK, gin.H{"status": "OK"})
		} else {
			c.JSON(http.StatusNotFound, gin.H{})
		}
	}
}