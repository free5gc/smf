package context

import (
	"free5gc/lib/openapi/models"
)

// TrafficControlData - Traffic control data defines how traffic data flows
// associated with a rule are treated (e.g. blocked, redirected).
type TrafficControlData struct {
	// shall include attribute
	TrafficControlID string

	// maybe include attribute
	FlowStatus     models.FlowStatus
	RouteToLocs    []models.RouteToLocation
	UpPathChgEvent *models.UpPathChgEvent

	// referenced dataType
	refedPCCRule map[string]*PCCRule
}

// NewTrafficControlDataFromModel - create the traffic control data from OpenAPI model
func NewTrafficControlDataFromModel(model *models.TrafficControlData) *TrafficControlData {
	trafficControlData := new(TrafficControlData)

	trafficControlData.TrafficControlID = model.TcId
	trafficControlData.FlowStatus = model.FlowStatus
	trafficControlData.RouteToLocs = model.RouteToLocs
	trafficControlData.UpPathChgEvent = model.UpPathChgEvent

	trafficControlData.refedPCCRule = make(map[string]*PCCRule)

	return trafficControlData
}

// GetRefedPCCRules - returns the PCCRules that reference this tcData
func (tc *TrafficControlData) GetRefedPCCRules() map[string]*PCCRule {
	return tc.refedPCCRule
}
