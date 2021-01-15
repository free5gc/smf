package pdusession_test

import (
	"context"
	"free5gc/lib/CommonConsumerTestData/SMF/TestPDUSession"
	"free5gc/lib/openapi/Nsmf_PDUSession"
	"free5gc/lib/openapi/models"
	"free5gc/src/smf/pdusession"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpdateSmContext(t *testing.T) {
	go pdusession.DummyServer()
	configuration := Nsmf_PDUSession.NewConfiguration()
	configuration.SetBasePath("https://127.0.0.10:29502")
	client := Nsmf_PDUSession.NewAPIClient(configuration)
	var request models.UpdateSmContextRequest

	table := TestPDUSession.ConsumerSMFPDUSessionUpdateContextTable["ACTIVATING"]
	request.JsonData = table.JsonData
	request.BinaryDataN1SmMessage = table.BinaryDataN1SmMessage

	_, httpRsp, _ := client.IndividualSMContextApi.UpdateSmContext(context.Background(), "123", request)
	assert.True(t, httpRsp != nil)
	assert.Equal(t, "404 Not Found", httpRsp.Status)

}
