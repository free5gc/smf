package consumer

import (
	"context"

	"github.com/antihax/optional"
	"github.com/pkg/errors"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/util"
)

func SDMGetSmData(smCtx *smf_context.SMContext,
	smPlmnID *models.PlmnId,
) (problemDetails *models.ProblemDetails, err error) {
	// Query UDM
	if problemDetails, err = SendNFDiscoveryUDM(); err != nil {
		smCtx.Log.Warnf("Send NF Discovery Serving UDM Error[%v]", err)
	} else if problemDetails != nil {
		smCtx.Log.Warnf("Send NF Discovery Serving UDM Problem[%+v]", problemDetails)
	} else {
		smCtx.Log.Infoln("Send NF Discovery Serving UDM Successfully")
	}

	smDataParams := &Nudm_SubscriberDataManagement.GetSmDataParamOpts{
		Dnn:         optional.NewString(smCtx.Dnn),
		PlmnId:      optional.NewInterface(openapi.MarshToJsonString(smPlmnID)),
		SingleNssai: optional.NewInterface(openapi.MarshToJsonString(smCtx.SNssai)),
	}

	SubscriberDataManagementClient := smf_context.GetSelf().SubscriberDataManagementClient

	sessSubData, rsp, localErr := SubscriberDataManagementClient.
		SessionManagementSubscriptionDataRetrievalApi.
		GetSmData(context.Background(), smCtx.Supi, smDataParams)
	if localErr == nil {
		defer func() {
			if rspCloseErr := rsp.Body.Close(); rspCloseErr != nil {
				logger.ConsumerLog.Errorf("GetSmData response body cannot close: %+v", rspCloseErr)
			}
		}()
		if len(sessSubData) > 0 {
			smCtx.DnnConfiguration = sessSubData[0].DnnConfigurations[smCtx.Dnn]
			// UP Security info present in session management subscription data
			if smCtx.DnnConfiguration.UpSecurity != nil {
				smCtx.UpSecurity = smCtx.DnnConfiguration.UpSecurity
			}
		} else {
			logger.ConsumerLog.Errorln("SessionManagementSubscriptionData from UDM is nil")
			err = openapi.ReportError("SmData is nil")
		}
	} else if rsp != nil {
		if rsp.Status != localErr.Error() {
			err = localErr
			return
		}
		problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
		problemDetails = &problem
	} else {
		logger.ConsumerLog.Errorln("Get SessionManagementSubscriptionData error:", localErr)
		err = localErr
	}

	return problemDetails, err
}

func SDMSubscribe(smCtx *smf_context.SMContext, smPlmnID *models.PlmnId) (
	problemDetails *models.ProblemDetails, err error,
) {
	if !smf_context.GetSelf().Ues.UeExists(smCtx.Supi) {
		sdmUri := util.SearchNFServiceUri(smf_context.GetSelf().UDMProfile, models.ServiceName_NUDM_SDM,
			models.NfServiceStatus_REGISTERED)
		if sdmUri == "" {
			return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
		}

		configuration := Nudm_SubscriberDataManagement.NewConfiguration()
		configuration.SetBasePath(sdmUri)
		client := Nudm_SubscriberDataManagement.NewAPIClient(configuration)

		sdmSubscription := models.SdmSubscription{
			NfInstanceId: smf_context.GetSelf().NfInstanceID,
			PlmnId:       smPlmnID,
		}

		resSubscription, httpResp, localErr := client.SubscriptionCreationApi.Subscribe(
			context.Background(), smCtx.Supi, sdmSubscription)
		defer func() {
			if httpResp != nil {
				if rspCloseErr := httpResp.Body.Close(); rspCloseErr != nil {
					logger.ConsumerLog.Errorf("Subscribe response body cannot close: %+v",
						rspCloseErr)
				}
			}
		}()

		if localErr == nil {
			smf_context.GetSelf().Ues.SetSubscriptionId(smCtx.Supi, resSubscription.SubscriptionId)
			logger.ConsumerLog.Infoln("SDM Subscription Successful UE:", smCtx.Supi, "SubscriptionId:",
				resSubscription.SubscriptionId)
		} else if httpResp != nil {
			if httpResp.Status != localErr.Error() {
				err = localErr
				return problemDetails, err
			}
			problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
			problemDetails = &problem
		} else {
			err = openapi.ReportError("server no response")
		}
	}

	smf_context.GetSelf().Ues.IncrementPduSessionCount(smCtx.Supi)
	return problemDetails, err
}

func SDMUnSubscribe(smCtx *smf_context.SMContext) (problemDetails *models.ProblemDetails,
	err error,
) {
	if smf_context.GetSelf().Ues.IsLastPduSession(smCtx.Supi) {
		sdmUri := util.SearchNFServiceUri(smf_context.GetSelf().UDMProfile, models.ServiceName_NUDM_SDM,
			models.NfServiceStatus_REGISTERED)
		if sdmUri == "" {
			return nil, errors.Errorf("SMF can not select an UDM by NRF: SearchNFServiceUri failed")
		}
		configuration := Nudm_SubscriberDataManagement.NewConfiguration()
		configuration.SetBasePath(sdmUri)

		client := Nudm_SubscriberDataManagement.NewAPIClient(configuration)

		subscriptionId := smf_context.GetSelf().Ues.GetSubscriptionId(smCtx.Supi)

		httpResp, localErr := client.SubscriptionDeletionApi.Unsubscribe(context.Background(), smCtx.Supi, subscriptionId)
		defer func() {
			if httpResp != nil {
				if rspCloseErr := httpResp.Body.Close(); rspCloseErr != nil {
					logger.ConsumerLog.Errorf("Unsubscribe response body cannot close: %+v",
						rspCloseErr)
				}
			}
		}()
		if localErr == nil {
			logger.ConsumerLog.Infoln("SDM UnSubscription Successful UE:", smCtx.Supi, "SubscriptionId:",
				subscriptionId)
			return problemDetails, err
		} else if httpResp != nil {
			if httpResp.Status != localErr.Error() {
				err = localErr
				return problemDetails, err
			}
			problem := localErr.(openapi.GenericOpenAPIError).Model().(models.ProblemDetails)
			problemDetails = &problem
		} else {
			err = openapi.ReportError("server no response")
		}
		smf_context.GetSelf().Ues.DeleteUe(smCtx.Supi)
	} else {
		smf_context.GetSelf().Ues.DecrementPduSessionCount(smCtx.Supi)
	}
	return problemDetails, err
}
