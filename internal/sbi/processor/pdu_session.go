package processor

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/antihax/optional"
	"github.com/gin-gonic/gin"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Nsmf_PDUSession"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
)

func (p *Processor) HandlePDUSessionSMContextCreate(
	c *gin.Context,
	request models.PostSmContextsRequest,
	isDone <-chan struct{},
) {
	// GSM State
	// PDU Session Establishment Accept/Reject
	var response models.PostSmContextsResponse
	response.JsonData = new(models.SmContextCreatedData)
	logger.PduSessLog.Traceln("In HandlePDUSessionSMContextCreate")

	// Check has PDU Session Establishment Request
	m := nas.NewMessage()
	if err := m.GsmMessageDecode(&request.BinaryDataN1SmMessage); err != nil ||
		m.GsmHeader.GetMessageType() != nas.MsgTypePDUSessionEstablishmentRequest {
		logger.PduSessLog.Warnln("GsmMessageDecode Error: ", err)
		postSmContextsError := models.PostSmContextsErrorResponse{
			JsonData: &models.SmContextCreateError{
				Error: &Nsmf_PDUSession.N1SmError,
			},
		}
		c.Render(http.StatusForbidden, openapi.MultipartRelatedRender{Data: postSmContextsError})
		return
	}

	createData := request.JsonData
	// Check duplicate SM Context
	if dup_smCtx := smf_context.GetSelf().GetSMContextById(createData.Supi, createData.PduSessionId); dup_smCtx != nil {
		p.HandlePDUSessionSMContextLocalRelease(dup_smCtx, createData)
	}

	upi := smf_context.GetUserPlaneInformation()
	upi.Mu.RLock()
	defer upi.Mu.RUnlock()

	// Discover UDM
	if problemDetails, err := p.Consumer().SendNFDiscoveryUDM(); err != nil {
		logger.PduSessLog.Errorf("Send NF Discovery Serving UDM Error[%v]", err)
		return
	} else if problemDetails != nil {
		logger.PduSessLog.Errorf("Send NF Discovery Serving UDM Problem[%+v]", problemDetails)
		return
	} else {
		logger.PduSessLog.Debugln("Send NF Discovery Serving UDM Successful")
	}

	// Query UDM for session management subscription data
	smDataParams := &Nudm_SubscriberDataManagement.GetSmDataParamOpts{
		Dnn:         optional.NewString(createData.Dnn),
		PlmnId:      optional.NewInterface(openapi.MarshToJsonString(createData.Guami.PlmnId)),
		SingleNssai: optional.NewInterface(openapi.MarshToJsonString(createData.SNssai)),
	}

	ctx, _, oauthErr := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NUDM_SDM, models.NfType_UDM)
	if oauthErr != nil {
		logger.PduSessLog.Errorf("Get Token Context Error[%v]", oauthErr)
		return
	}

	sessSubData, rsp, err := p.Consumer().GetSmData(ctx, createData.Supi, smDataParams)
	if err != nil {
		logger.PduSessLog.Errorln("Get SessionManagementSubscriptionData error:", err)
		return
	} else {
		defer func() {
			if rspCloseErr := rsp.Body.Close(); rspCloseErr != nil {
				logger.PduSessLog.Errorf("GetSmData response body cannot close: %+v", rspCloseErr)
				return
			}
		}()
		if len(sessSubData) == 0 {
			logger.PduSessLog.Errorf("No SessionManagementSubscriptionData registered in UDM for requested session %+v",
				smDataParams)
			return
		}
	}

	// create session management context object
	smContext := smf_context.NewSMContext(createData, sessSubData)
	smContext.SetState(smf_context.ActivePending)

	// TODO: from here on, RELEASE sm context after error before returning!

	if err := HandlePDUSessionEstablishmentRequest(smContext, m.PDUSessionEstablishmentRequest); err != nil {
		smContext.Log.Errorf("PDU Session Establishment fail by %s", err)
		gsmError := &GSMError{}
		if errors.As(err, &gsmError) {
			p.makeEstRejectResAndReleaseSMContext(c, smContext,
				gsmError.GSMCause,
				&Nsmf_PDUSession.N1SmError)
			return
		}
		p.makeEstRejectResAndReleaseSMContext(c, smContext,
			nasMessage.Cause5GSMRequestRejectedUnspecified,
			&Nsmf_PDUSession.N1SmError)
		return
	}

	// Discover and set AMF for later use
	servingAMF, problemDetails, err := p.Consumer().SendNFDiscoveryServingAMF(smContext.ServingNfId)
	if err != nil {
		logger.PduSessLog.Warnf("Send NF Discovery Serving AMF Error[%v]", err)
	} else if problemDetails != nil {
		logger.PduSessLog.Warnf("Send NF Discovery Serving AMF Problem[%+v]", problemDetails)
	} else {
		logger.PduSessLog.Traceln("Send NF Discovery Serving AMF successfully")
		if err := smContext.SetServingAMF(servingAMF); err != nil {
			logger.PduSessLog.Warnf("Set serving AMF error[%v]", err)
		}
	}

	// choose PSA and allocate UE IP address (or configure static IP)
	// also selects PSA in ULCL scenarios
	if err := smContext.FindPSAandAllocUeIP(); err != nil {
		smContext.SetState(smf_context.InActive)
		smContext.Log.Errorf("PDUSessionSMContextCreate err: %v", err)
		p.makeEstRejectResAndReleaseSMContext(c, smContext,
			nasMessage.Cause5GSMInsufficientResourcesForSpecificSliceAndDNN,
			&Nsmf_PDUSession.InsufficientResourceSliceDnn)
		return
	}

	// Discover and set PCF for later use
	servingPCF, err := p.Consumer().PCFSelection()
	if err != nil {
		logger.PduSessLog.Warnf("Send NF Discovery PCF Error[%v]", err)
	} else {
		logger.PduSessLog.Traceln("Send NF Discovery PCF successful")
		if err := smContext.SetServingPCF(servingPCF); err != nil {
			logger.PduSessLog.Warnf("Set serving PCF error[%v]", err)
		}
	}

	smPolicyID, smPolicyDecision, err := p.Consumer().SendSMPolicyAssociationCreate(smContext)
	if err != nil {
		if openapiError, ok := err.(openapi.GenericOpenAPIError); ok {
			problemDetails := openapiError.Model().(models.ProblemDetails)
			smContext.Log.Errorln("setup sm policy association failed:", err, problemDetails)
			smContext.SetState(smf_context.InActive)
			if problemDetails.Cause == "USER_UNKNOWN" {
				p.makeEstRejectResAndReleaseSMContext(c, smContext,
					nasMessage.Cause5GSMRequestRejectedUnspecified,
					&Nsmf_PDUSession.SubscriptionDenied)
				return
			}
		}
		p.makeEstRejectResAndReleaseSMContext(c, smContext,
			nasMessage.Cause5GSMNetworkFailure,
			&Nsmf_PDUSession.NetworkFailure)
		return
	}
	smContext.SMPolicyID = smPolicyID

	// PDU　session create is a charging event
	logger.PduSessLog.Debugf("CHF Selection for SMContext SUPI[%s] PDUSessionID[%d]\n",
		smContext.Supi, smContext.PDUSessionID)
	servingCHF, err := p.Consumer().CHFSelection()
	if err != nil {
		logger.PduSessLog.Warnf("Send NF Discovery CHF Error[%v]", err)
	} else {
		logger.PduSessLog.Traceln("Send NF Discovery CHF successful")
		if err := smContext.SetServingCHF(servingCHF); err != nil {
			logger.PduSessLog.Warnf("Set serving CHF error[%v]", err)
		}
		p.CreateChargingSession(smContext)
	}

	// Update SessionRule from decision
	if err = smContext.ApplySessionRules(smPolicyDecision); err != nil {
		smContext.Log.Errorf("PDUSessionSMContextCreate err: %v", err)
		p.makeEstRejectResAndReleaseSMContext(c, smContext,
			nasMessage.Cause5GSMRequestRejectedUnspecified,
			&Nsmf_PDUSession.SubscriptionDenied)
		return
	}

	// If PCF prepares default Pcc Rule, SMF does not need to create defaultDataPath
	if err = smContext.ApplyPccRules(smPolicyDecision); err != nil {
		smContext.Log.Errorf("apply sm policy decision error: %+v", err)
	}

	// creates a default data path if it was not created in previous step
	if err = smContext.SelectDefaultDataPath(); err != nil {
		smContext.SetState(smf_context.InActive)
		smContext.Log.Errorf("PDUSessionSMContextCreate err: %v", err)
		p.makeEstRejectResAndReleaseSMContext(c, smContext,
			nasMessage.Cause5GSMInsufficientResourcesForSpecificSliceAndDNN,
			&Nsmf_PDUSession.InsufficientResourceSliceDnn)
		return
	}

	// ULCL scenario: add second PSA UPF and prepare data path and PDRs
	if smf_context.GetSelf().ULCLSupport && smf_context.CheckUEHasPreConfig(smContext.Supi) {
		bpMGR := smContext.BPManager
		if bpMGR == nil {
			smContext.SetState(smf_context.InActive)
			smContext.Log.Errorln("PDUSessionSMContextCreate err: BPManager is nil in ULCL senario")
			p.makeEstRejectResAndReleaseSMContext(c, smContext,
				nasMessage.Cause5GSMInsufficientResourcesForSpecificSliceAndDNN,
				&Nsmf_PDUSession.InsufficientResourceSliceDnn)
			return
		}

		/*	TODO: can we integrate ULCL UPLINK data path establishment here as well?
			// or does it have to happen during session modification?

			switch bpMGR.AddingPSAState {
			case smf_context.ActivatingDataPath:
				for {
					// select second PSA UPF
					bpMGR.SelectPSA2(smContext) // sets bpMGR.ActivatingPath and bpMGR.ActivatedPaths
					if len(bpMGR.ActivatedPaths) == len(smContext.Tunnel.DataPathPool) {
						break
					}
					smContext.AllocateLocalSEIDForDataPath(bpMGR.ActivatingPath)

					// select a UPF as ULCL
					err := bpMGR.FindULCL(smContext)
					if err != nil {
						smContext.SetState(smf_context.InActive)
						smContext.Log.Errorf("PDUSessionSMContextCreate err: %v", err)
						p.makeEstRejectResAndReleaseSMContext(c, smContext,
							nasMessage.Cause5GSMInsufficientResourcesForSpecificSliceAndDNN,
							&Nsmf_PDUSession.InsufficientResourceSliceDnn)
						return
					}

					// allocate path PDR and TEID
					bpMGR.ActivatingPath.ActivateTunnelAndPDR(smContext, 255)
					// N1N2MessageTransfer here

					// TODO: does this trigger a charging event? or only sending the PFCP message?
				}
			default:
				smContext.SetState(smf_context.InActive)
				smContext.Log.Errorln("PDUSessionSMContextCreate err: BPManager is in unexpected state")
				p.makeEstRejectResAndReleaseSMContext(c, smContext,
					nasMessage.Cause5GSMInsufficientResourcesForSpecificSliceAndDNN,
					&Nsmf_PDUSession.InsufficientResourceSliceDnn)
				return
			}
		*/
	}

	// generate goroutine to handle PFCP and
	// reply PDUSessionSMContextCreate rsp immediately
	// this establishes the UPLINK data path direction only (DOWNLINK in sess. modification)
	go func() {
		smContext.SendUpPathChgNotification("EARLY", SendUpPathChgEventExposureNotification)

		pfcpReturnState := p.ActivatePDUSessionAtUPFs(smContext)
		switch pfcpReturnState {
		case smf_context.SessionEstablishSuccess:
			p.sendPDUSessionEstablishmentAccept(smContext)
		case smf_context.SessionEstablishFailed:
			p.sendPDUSessionEstablishmentReject(smContext, nasMessage.Cause5GSMNetworkFailure)
		}

		smContext.SendUpPathChgNotification("LATE", SendUpPathChgEventExposureNotification)

		smContext.PostRemoveDataPath()
	}()

	response.JsonData = smContext.BuildCreatedData()
	c.Header("Location", smContext.Ref)
	c.Render(http.StatusCreated, openapi.MultipartRelatedRender{Data: response})
}

func (p *Processor) HandlePDUSessionSMContextUpdate(
	c *gin.Context,
	body models.UpdateSmContextRequest,
	smContextRef string,
) {
	// GSM State
	// PDU Session Modification Reject(Cause Value == 43 || Cause Value != 43)/Complete
	// PDU Session Release Command/Complete
	var buf []byte
	var n2Buf []byte
	var err error

	smContext := smf_context.GetSelf().GetSMContextByRef(smContextRef)

	upi := smf_context.GetUserPlaneInformation()
	upi.Mu.RLock()
	defer upi.Mu.RUnlock()

	if smContext == nil {
		logger.PduSessLog.Warnf("SMContext[%s] is not found", smContextRef)

		updateSmContextError := models.UpdateSmContextErrorResponse{
			JsonData: &models.SmContextUpdateError{
				UpCnxState: models.UpCnxState_DEACTIVATED,
				Error: &models.ProblemDetails{
					Type:   "Resource Not Found",
					Title:  "SMContext Ref is not found",
					Status: http.StatusNotFound,
				},
			},
		}
		c.JSON(http.StatusNotFound, updateSmContextError)
		return
	}

	var sendPFCPModification bool
	var pfcpResponseStatus smf_context.PFCPSessionResponseStatus
	var response models.UpdateSmContextResponse
	response.JsonData = new(models.SmContextUpdatedData)

	smContextUpdateData := body.JsonData

	if body.BinaryDataN1SmMessage != nil {
		m := nas.NewMessage()
		err = m.GsmMessageDecode(&body.BinaryDataN1SmMessage)
		smContext.Log.Tracef("N1 Message: %s", hex.EncodeToString(body.BinaryDataN1SmMessage))
		if err != nil {
			smContext.Log.Errorf("N1 Message parse failed: %v", err)
			updateSmContextError := models.UpdateSmContextErrorResponse{
				JsonData: &models.SmContextUpdateError{
					Error: &Nsmf_PDUSession.N1SmError,
				},
			} // Depends on the reason why N4 fail
			c.JSON(http.StatusForbidden, updateSmContextError)
			return
		}

		switch m.GsmHeader.GetMessageType() {
		case nas.MsgTypePDUSessionReleaseRequest:
			smContext.CheckState(smf_context.Active)
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture

			HandlePDUSessionReleaseRequest(smContext, m.PDUSessionReleaseRequest)
			if smContext.SelectedUPF != nil && smContext.PDUAddress != nil {
				smContext.Log.Infof("Release IP[%s]", smContext.PDUAddress)
				upi.ReleaseUEIP(smContext.SelectedUPF, smContext.PDUAddress, smContext.UseStaticIP)
				smContext.PDUAddress = nil
				// keep SelectedUPF until PDU Session Release is completed
			}

			// remove SM Policy Association
			if smContext.SMPolicyID != "" {
				if err = p.Consumer().SendSMPolicyAssociationTermination(smContext); err != nil {
					smContext.Log.Errorf("SM Policy Termination failed: %s", err)
				} else {
					smContext.SMPolicyID = ""
				}
			}

			if smContext.UeCmRegistered {
				problemDetails, errUeCmDeregistration := p.Consumer().UeCmDeregistration(smContext)
				if problemDetails != nil {
					if problemDetails.Cause != CONTEXT_NOT_FOUND {
						logger.PduSessLog.Errorf("UECM_DeRegistration Failed Problem[%+v]", problemDetails)
					}
				} else if errUeCmDeregistration != nil {
					logger.PduSessLog.Errorf("UECM_DeRegistration Error[%+v]", errUeCmDeregistration)
				} else {
					logger.PduSessLog.Traceln("UECM_DeRegistration successful")
				}
			}

			cause := nasMessage.Cause5GSMRegularDeactivation
			if m.PDUSessionReleaseRequest.Cause5GSM != nil {
				cause = m.PDUSessionReleaseRequest.Cause5GSM.GetCauseValue()
			}

			if buf, err = smf_context.
				BuildGSMPDUSessionReleaseCommand(smContext, cause, true); err != nil {
				smContext.Log.Errorf("Build GSM PDUSessionReleaseCommand failed: %+v", err)
			} else {
				response.BinaryDataN1SmMessage = buf
				response.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseCommand"}
				p.sendGSMPDUSessionReleaseCommand(smContext, buf)
			}

			if buf, err = smf_context.
				BuildPDUSessionResourceReleaseCommandTransfer(smContext); err != nil {
				smContext.Log.Errorf("Build PDUSessionResourceReleaseCommandTransfer failed: %+v", err)
			} else {
				response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_REL_CMD
				response.BinaryDataN2SmInformation = buf
				response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUResourceReleaseCommand"}
			}

			smContext.SetState(smf_context.PFCPModification)

			pfcpResponseStatus = p.ReleaseSessionAtUPFs(smContext)
		case nas.MsgTypePDUSessionReleaseComplete:
			smContext.CheckState(smf_context.InActivePending)
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture

			smContext.SetState(smf_context.InActive)
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
			smContext.StopT3592()

			// If CN tunnel resource is released, should
			if smContext.Tunnel.ANInformation.IPAddress == nil {
				p.removeSMContextFromAllNF(smContext, true)
			}
		case nas.MsgTypePDUSessionModificationRequest:
			logger.PduSessLog.Infoln("Handle PDU session modification request")
			if rsp, errHandleReq := p.
				HandlePDUSessionModificationRequest(smContext, m.PDUSessionModificationRequest); errHandleReq != nil {
				if buf, err = smf_context.BuildGSMPDUSessionModificationReject(smContext); err != nil {
					smContext.Log.Errorf("build GSM PDUSessionModificationReject failed: %+v", err)
				} else {
					response.BinaryDataN1SmMessage = buf
				}
			} else {
				if buf, err = rsp.PlainNasEncode(); err != nil {
					smContext.Log.Errorf("build GSM PDUSessionModificationCommand failed: %+v", err)
				} else {
					response.BinaryDataN1SmMessage = buf
					p.sendGSMPDUSessionModificationCommand(smContext, buf)
				}
			}

			if buf, err = smf_context.BuildPDUSessionResourceModifyRequestTransfer(smContext); err != nil {
				smContext.Log.Errorf("build N2 BuildPDUSessionResourceModifyRequestTransfer failed: %v", err)
			} else {
				response.BinaryDataN2SmInformation = buf
				response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDU_RES_MOD"}
				response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_MOD_REQ
			}

			response.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionModificationReject"}
			c.Render(http.StatusOK, openapi.MultipartRelatedRender{Data: response})
			return
		case nas.MsgTypePDUSessionModificationComplete:
			smContext.StopT3591()
		case nas.MsgTypePDUSessionModificationReject:
			smContext.StopT3591()
		}
	}

	tunnel := smContext.Tunnel
	urrList := []*smf_context.URR{}

	switch smContextUpdateData.UpCnxState {
	case models.UpCnxState_ACTIVATING:
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		smContext.SetState(smf_context.ModificationPending)
		response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUSessionResourceSetupRequestTransfer"}
		response.JsonData.UpCnxState = models.UpCnxState_ACTIVATING
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ

		n2Buf, err = smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext)
		if err != nil {
			logger.PduSessLog.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		} else {
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUSessionResourceSetupRequestTransfer"}
		}
		smContext.UpCnxState = models.UpCnxState_ACTIVATING
	case models.UpCnxState_DEACTIVATED:
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		// If the PDU session has been released, skip sending PFCP Session Modification Request
		if smContext.CheckState(smf_context.InActivePending) {
			logger.CtxLog.Infof("Skip sending PFCP Session Modification Request of PDUSessionID:%d of SUPI:%s",
				smContext.PDUSessionID, smContext.Supi)
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
			c.Render(http.StatusOK, openapi.MultipartRelatedRender{Data: response})
			return
		}
		smContext.SetState(smf_context.ModificationPending)
		response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
		smContext.UpCnxState = body.JsonData.UpCnxState
		// UE location change is a charging event
		// TODO: This is not tested yet
		if smContext.UeLocation != body.JsonData.UeLocation {
			// All rating group related to this Pdu session should send charging request
			for _, dataPath := range tunnel.DataPathPool {
				if dataPath.Activated {
					for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
						if curDataPathNode.IsANUPF() {
							urrList = append(urrList, curDataPathNode.UpLinkTunnel.PDR.URR...)
							p.QueryReport(smContext, curDataPathNode.UPF, urrList, models.TriggerType_USER_LOCATION_CHANGE)
						}
					}
				}
			}

			p.ReportUsageAndUpdateQuota(smContext)
		}

		smContext.UeLocation = body.JsonData.UeLocation

		// Set FAR and An, N3 Release Info
		// TODO: Deactivate all datapath in ANUPF
		for _, dataPath := range smContext.Tunnel.DataPathPool {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR
			if DLPDR == nil {
				smContext.Log.Warnf("Access network resource is released")
			} else {
				DLPDR.FAR.SetState(smf_context.RULE_UPDATE)
				DLPDR.FAR.ApplyAction.Forw = false
				DLPDR.FAR.ApplyAction.Buff = true
				DLPDR.FAR.ApplyAction.Nocp = true
				sendPFCPModification = true
				smContext.SetState(smf_context.PFCPModification)
			}
		}
	}

	switch smContextUpdateData.N2SmInfoType {
	case models.N2SmInfoType_PDU_RES_SETUP_RSP:
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		smContext.SetState(smf_context.ModificationPending)

		for _, dataPath := range tunnel.DataPathPool {
			if dataPath.Activated {
				ANUPF := dataPath.FirstDPNode
				DLPDR := ANUPF.DownLinkTunnel.PDR

				DLPDR.FAR.ApplyAction = pfcpType.ApplyAction{
					Buff: false,
					Drop: false,
					Dupl: false,
					Forw: true,
					Nocp: false,
				}
				DLPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
					DestinationInterface: pfcpType.DestinationInterface{
						InterfaceValue: pfcpType.DestinationInterfaceAccess,
					},
					NetworkInstance: &pfcpType.NetworkInstance{
						NetworkInstance: smContext.Dnn,
						FQDNEncoding:    factory.SmfConfig.Configuration.NwInstFqdnEncoding,
					},
				}

				DLPDR.SetState(smf_context.RULE_UPDATE)
				DLPDR.FAR.SetState(smf_context.RULE_UPDATE)
			}
		}

		if err = smf_context.
			HandlePDUSessionResourceSetupResponseTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			smContext.Log.Errorf("Handle PDUSessionResourceSetupResponseTransfer failed: %+v", err)
		}
		sendPFCPModification = true
		smContext.SetState(smf_context.PFCPModification)
	case models.N2SmInfoType_PDU_RES_SETUP_FAIL:
		if err = smf_context.
			HandlePDUSessionResourceSetupUnsuccessfulTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			smContext.Log.Errorf("Handle PDUSessionResourceSetupResponseTransfer failed: %+v", err)
		}
	case models.N2SmInfoType_PDU_RES_MOD_RSP:
		if err = smf_context.
			HandlePDUSessionResourceModifyResponseTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			smContext.Log.Errorf("Handle PDUSessionResourceModifyResponseTransfer failed: %+v", err)
		}
	case models.N2SmInfoType_PDU_RES_REL_RSP:
		// remove an tunnel info
		smContext.Log.Infoln("Handle N2 PDU Resource Release Response")
		smContext.Tunnel.ANInformation = &smf_context.ANInformation{
			IPAddress: nil,
			TEID:      0,
		}

		if smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID {
			smContext.CheckState(smf_context.InActivePending)
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			smContext.Log.Infoln("Release_DUE_TO_DUP_PDU_ID: Send Update SmContext Response")
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
			// If NAS layer is inActive, the context should be remove
			if smContext.CheckState(smf_context.InActive) {
				p.removeSMContextFromAllNF(smContext, true)
			}
		} else if smContext.CheckState(smf_context.InActive) { // normal case
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture

			// If N1 PDU Session Release Complete is received, smContext state is InActive.
			// Remove SMContext when receiving N2 PDU Resource Release Response.
			// Use go routine to send Notification to prevent blocking the handling process
			p.removeSMContextFromAllNF(smContext, true)
		}
	case models.N2SmInfoType_PATH_SWITCH_REQ:
		smContext.Log.Traceln("Handle Path Switch Request")
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		smContext.SetState(smf_context.ModificationPending)

		if err = smf_context.HandlePathSwitchRequestTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			smContext.Log.Errorf("Handle PathSwitchRequestTransfer: %+v", err)
		}

		if n2Buf, err = smf_context.BuildPathSwitchRequestAcknowledgeTransfer(smContext); err != nil {
			smContext.Log.Errorf("Build Path Switch Transfer Error(%+v)", err)
		} else {
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PATH_SWITCH_REQ_ACK
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfo = &models.RefToBinaryData{
				ContentId: "PATH_SWITCH_REQ_ACK",
			}
		}

		sendPFCPModification = true
		smContext.SetState(smf_context.PFCPModification)
	case models.N2SmInfoType_PATH_SWITCH_SETUP_FAIL:
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		smContext.SetState(smf_context.ModificationPending)
		err = smf_context.HandlePathSwitchRequestSetupFailedTransfer(
			body.BinaryDataN2SmInformation, smContext)
		if err != nil {
			smContext.Log.Errorf("HandlePathSwitchRequestSetupFailedTransfer failed: %v", err)
		}
	case models.N2SmInfoType_HANDOVER_REQUIRED:
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture
		smContext.SetState(smf_context.ModificationPending)
		response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "Handover"}
	}

	switch smContextUpdateData.HoState {
	case models.HoState_PREPARING:
		smContext.Log.Traceln("In HoState_PREPARING")
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		smContext.SetState(smf_context.ModificationPending)
		smContext.HoState = models.HoState_PREPARING
		err = smf_context.HandleHandoverRequiredTransfer(
			body.BinaryDataN2SmInformation, smContext)
		if err != nil {
			smContext.Log.Errorf("Handle HandoverRequiredTransfer failed: %+v", err)
		}
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ

		if n2Buf, err = smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext); err != nil {
			smContext.Log.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		} else {
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ
			response.JsonData.N2SmInfo = &models.RefToBinaryData{
				ContentId: "PDU_RES_SETUP_REQ",
			}
		}
		response.JsonData.HoState = models.HoState_PREPARING
	case models.HoState_PREPARED:
		smContext.Log.Traceln("In HoState_PREPARED")
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		smContext.SetState(smf_context.ModificationPending)
		smContext.HoState = models.HoState_PREPARED
		response.JsonData.HoState = models.HoState_PREPARED
		err = smf_context.HandleHandoverRequestAcknowledgeTransfer(
			body.BinaryDataN2SmInformation, smContext)
		if err != nil {
			smContext.Log.Errorf("Handle HandoverRequestAcknowledgeTransfer failed: %+v", err)
		}

		// request UPF establish indirect forwarding path for DL
		if smContext.DLForwardingType == smf_context.IndirectForwarding {
			ANUPF := smContext.IndirectForwardingTunnel.FirstDPNode
			IndirectForwardingPDR := ANUPF.UpLinkTunnel.PDR

			// release indirect forwading path
			if err = ANUPF.UPF.RemovePDR(IndirectForwardingPDR); err != nil {
				logger.PduSessLog.Errorln("release indirect path: ", err)
			}

			sendPFCPModification = true
			smContext.SetState(smf_context.PFCPModification)
		}

		if n2Buf, err = smf_context.BuildHandoverCommandTransfer(smContext); err != nil {
			smContext.Log.Errorf("Build HandoverCommandTransfer failed: %v", err)
		} else {
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfoType = models.N2SmInfoType_HANDOVER_CMD
			response.JsonData.N2SmInfo = &models.RefToBinaryData{
				ContentId: "HANDOVER_CMD",
			}
		}
		response.JsonData.HoState = models.HoState_PREPARING
	case models.HoState_COMPLETED:
		smContext.Log.Traceln("In HoState_COMPLETED")
		smContext.CheckState(smf_context.Active)
		// Wait till the state becomes Active again
		// TODO: implement sleep wait in concurrent architecture

		// remove indirect forwarding path
		if smContext.DLForwardingType == smf_context.IndirectForwarding {
			indirectForwardingPDR := smContext.IndirectForwardingTunnel.FirstDPNode.GetUpLinkPDR()
			indirectForwardingPDR.SetState(smf_context.RULE_REMOVE)
			indirectForwardingPDR.FAR.SetState(smf_context.RULE_REMOVE)
		}

		sendPFCPModification = true
		smContext.SetState(smf_context.PFCPModification)
		smContext.HoState = models.HoState_COMPLETED
		response.JsonData.HoState = models.HoState_COMPLETED
	}

	if smContextUpdateData.Cause == models.Cause_REL_DUE_TO_DUPLICATE_SESSION_ID {
		// * release PDU Session Here
		smContext.Log.Infoln("[SMF] Cause_REL_DUE_TO_DUPLICATE_SESSION_ID")
		if smContext.CheckState(smf_context.Active) {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.State().String())
		}

		smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID = true

		switch smContext.State() {
		case smf_context.ActivePending, smf_context.ModificationPending, smf_context.Active:
			if buf, err = smf_context.BuildPDUSessionResourceReleaseCommandTransfer(smContext); err != nil {
				smContext.Log.Errorf("Build PDUSessionResourceReleaseCommandTransfer failed: %v", err)
			} else {
				response.BinaryDataN2SmInformation = buf
				response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_REL_CMD
				response.JsonData.N2SmInfo = &models.RefToBinaryData{
					ContentId: "PDUResourceReleaseCommand",
				}
			}

			smContext.SetState(smf_context.PFCPModification)

			pfcpResponseStatus = p.ReleaseSessionAtUPFs(smContext)
		default:
			smContext.Log.Infof("Not needs to send pfcp release")
		}

		smContext.Log.Infoln("[SMF] Cause_REL_DUE_TO_DUPLICATE_SESSION_ID")
	}

	// Check FSM and take corresponding action
	switch smContext.State() {
	case smf_context.PFCPModification:
		smContext.Log.Traceln("In case PFCPModification")

		if sendPFCPModification {
			pfcpResponseStatus = p.UpdatePDUSessionAtANUPF(smContext)
		}

		for upf, pfcp := range smContext.PFCPSessionContexts {
			logger.PduSessLog.Tracef("After session modification: UPF %s has PFCP session context %s",
				upf, pfcp)
		}

		switch pfcpResponseStatus {
		case smf_context.SessionUpdateSuccess:
			smContext.Log.Traceln("In case SessionUpdateSuccess")
			smContext.SetState(smf_context.Active)
			c.Render(http.StatusOK, openapi.MultipartRelatedRender{Data: response})
		case smf_context.SessionUpdateFailed:
			smContext.Log.Traceln("In case SessionUpdateFailed")
			smContext.SetState(smf_context.Active)
			// It is just a template
			updateSmContextError := models.UpdateSmContextErrorResponse{
				JsonData: &models.SmContextUpdateError{
					Error: &Nsmf_PDUSession.N1SmError,
				},
			} // Depends on the reason why N4 failed
			c.JSON(http.StatusForbidden, updateSmContextError)

		case smf_context.SessionReleaseSuccess:
			p.ReleaseChargingSession(smContext)

			smContext.Log.Traceln("In case SessionReleaseSuccess")
			smContext.SetState(smf_context.InActivePending)
			c.Render(http.StatusOK, openapi.MultipartRelatedRender{Data: response})

		case smf_context.SessionReleaseFailed:
			// Update SmContext Request(N1 PDU Session Release Request)
			// Send PDU Session Release Reject
			smContext.Log.Traceln("In case SessionReleaseFailed")
			problemDetail := models.ProblemDetails{
				Status: http.StatusInternalServerError,
				Cause:  "SYSTEM_FAILURE",
			}
			smContext.SetState(smf_context.Active)
			errResponse := models.UpdateSmContextErrorResponse{
				JsonData: &models.SmContextUpdateError{
					Error: &problemDetail,
				},
			}
			if smContextUpdateData.Cause != models.Cause_REL_DUE_TO_DUPLICATE_SESSION_ID {
				if buf, err = smf_context.BuildGSMPDUSessionReleaseReject(smContext); err != nil {
					logger.PduSessLog.Errorf("build GSM PDUSessionReleaseReject failed: %+v", err)
				} else {
					errResponse.BinaryDataN1SmMessage = buf
					errResponse.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseReject"}
				}
			}
			c.JSON(int(problemDetail.Status), errResponse)
		default:
			logger.PduSessLog.Errorf("Returned from UpdatePDUSessionAtANUPF with unexpected status %s", pfcpResponseStatus)

			smContext.SetState(smf_context.Active)

			// It is just a template
			updateSmContextError := models.UpdateSmContextErrorResponse{
				JsonData: &models.SmContextUpdateError{
					Error: &models.ProblemDetails{
						Status: http.StatusInternalServerError,
						Cause:  "SYSTEM_FAILURE",
					},
				},
			} // Depends on the reason why N4 fail
			c.JSON(http.StatusForbidden, updateSmContextError)
		}
		smContext.PostRemoveDataPath()
	case smf_context.ModificationPending:
		smContext.Log.Traceln("In case ModificationPending")
		smContext.SetState(smf_context.Active)
		c.Render(http.StatusOK, openapi.MultipartRelatedRender{Data: response})
	case smf_context.InActive, smf_context.InActivePending:
		smContext.Log.Traceln("In case InActive, InActivePending")
		c.Render(http.StatusOK, openapi.MultipartRelatedRender{Data: response})
	default:
		c.Render(http.StatusOK, openapi.MultipartRelatedRender{Data: response})
	}

	if smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID {
		// Note:
		// We don't want to launch timer to wait for N2SmInfoType_PDU_RES_REL_RSP.
		// So, local release smCtx and notify AMF after sending PDUSessionResourceReleaseCommand
		p.removeSMContextFromAllNF(smContext, true)
	}
}

func (p *Processor) HandlePDUSessionSMContextRelease(
	c *gin.Context,
	body models.ReleaseSmContextRequest,
	smContextRef string,
) {
	logger.PduSessLog.Traceln("In HandlePDUSessionSMContextRelease")
	smContext := smf_context.GetSelf().GetSMContextByRef(smContextRef)

	if smContext == nil {
		logger.PduSessLog.Warnf("SMContext[%s] is not found", smContextRef)

		updateSmContextError := &models.UpdateSmContextErrorResponse{
			JsonData: &models.SmContextUpdateError{
				UpCnxState: models.UpCnxState_DEACTIVATED,
				Error: &models.ProblemDetails{
					Type:   "Resource Not Found",
					Title:  "SMContext Ref is not found",
					Status: http.StatusNotFound,
				},
			},
		}
		c.JSON(http.StatusNotFound, updateSmContextError)
		return
	}

	smContext.StopT3591()
	smContext.StopT3592()

	// remove SM Policy Association
	if smContext.SMPolicyID != "" {
		if err := p.Consumer().SendSMPolicyAssociationTermination(smContext); err != nil {
			smContext.Log.Errorf("SM Policy Termination failed: %s", err)
		} else {
			smContext.SMPolicyID = ""
		}
	}

	if smContext.UeCmRegistered {
		problemDetails, err := p.Consumer().UeCmDeregistration(smContext)
		if problemDetails != nil {
			if problemDetails.Cause != CONTEXT_NOT_FOUND {
				logger.PduSessLog.Errorf("UECM_DeRegistration Failed Problem[%+v]", problemDetails)
			}
		} else if err != nil {
			logger.PduSessLog.Errorf("UECM_DeRegistration Error[%+v]", err)
		} else {
			logger.PduSessLog.Traceln("UECM_DeRegistration successful")
		}
	}

	if !smContext.CheckState(smf_context.InActive) {
		smContext.SetState(smf_context.PFCPModification)
	}
	pfcpResponseStatus := p.ReleaseSessionAtUPFs(smContext)

	switch pfcpResponseStatus {
	case smf_context.SessionReleaseSuccess:
		p.ReleaseChargingSession(smContext)

		smContext.Log.Traceln("In case SessionReleaseSuccess")
		smContext.SetState(smf_context.InActive)
		c.Status(http.StatusNoContent)

	case smf_context.SessionReleaseFailed:
		// Update SmContext Request(N1 PDU Session Release Request)
		// Send PDU Session Release Reject
		smContext.Log.Traceln("In case SessionReleaseFailed")
		problemDetail := models.ProblemDetails{
			Status: http.StatusInternalServerError,
			Cause:  "SYSTEM_FAILURE",
		}
		smContext.SetState(smf_context.Active)
		errResponse := models.UpdateSmContextErrorResponse{
			JsonData: &models.SmContextUpdateError{
				Error: &problemDetail,
			},
		}
		if buf, err := smf_context.BuildGSMPDUSessionReleaseReject(smContext); err != nil {
			smContext.Log.Errorf("Build GSM PDUSessionReleaseReject failed: %+v", err)
		} else {
			errResponse.BinaryDataN1SmMessage = buf
			errResponse.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseReject"}
		}

		c.JSON(int(problemDetail.Status), errResponse)

	default:
		smContext.Log.Warnf("The state shouldn't be [%s]\n", pfcpResponseStatus)

		smContext.Log.Traceln("In case Unknown")
		problemDetail := models.ProblemDetails{
			Status: http.StatusInternalServerError,
			Cause:  "SYSTEM_FAILURE",
		}
		smContext.SetState(smf_context.Active)
		errResponse := models.UpdateSmContextErrorResponse{
			JsonData: &models.SmContextUpdateError{
				Error: &problemDetail,
			},
		}
		if buf, err := smf_context.BuildGSMPDUSessionReleaseReject(smContext); err != nil {
			smContext.Log.Errorf("Build GSM PDUSessionReleaseReject failed: %+v", err)
		} else {
			errResponse.BinaryDataN1SmMessage = buf
			errResponse.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseReject"}
		}

		c.JSON(int(problemDetail.Status), errResponse)
	}

	p.removeSMContextFromAllNF(smContext, false)
}

func (p *Processor) HandlePDUSessionSMContextLocalRelease(
	smContext *smf_context.SMContext, createData *models.SmContextCreateData,
) {
	// remove SM Policy Association
	if smContext.SMPolicyID != "" {
		if err := p.Consumer().SendSMPolicyAssociationTermination(smContext); err != nil {
			logger.PduSessLog.Errorf("SM Policy Termination failed: %s", err)
		} else {
			smContext.SMPolicyID = ""
		}
	}

	if smContext.UeCmRegistered {
		problemDetails, err := p.Consumer().UeCmDeregistration(smContext)
		if problemDetails != nil {
			if problemDetails.Cause != CONTEXT_NOT_FOUND {
				logger.PduSessLog.Errorf("UECM_DeRegistration Failed Problem[%+v]", problemDetails)
			}
		} else if err != nil {
			logger.PduSessLog.Errorf("UECM_DeRegistration Error[%+v]", err)
		} else {
			logger.PduSessLog.Traceln("UECM_DeRegistration successful")
		}
	}

	smContext.SetState(smf_context.PFCPModification)

	pfcpResponseStatus := p.ReleaseSessionAtUPFs(smContext)

	switch pfcpResponseStatus {
	case smf_context.SessionReleaseSuccess:
		p.ReleaseChargingSession(smContext)

		logger.CtxLog.Traceln("In case SessionReleaseSuccess")
		smContext.SetState(smf_context.InActivePending)
		if createData.SmContextStatusUri != smContext.SmStatusNotifyUri {
			problemDetails, err := p.Consumer().SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
			if problemDetails != nil || err != nil {
				if problemDetails != nil {
					logger.PduSessLog.Warnf("Send SMContext Status Notification Problem[%+v]", problemDetails)
				}

				if err != nil {
					logger.PduSessLog.Warnf("Send SMContext Status Notification Error[%v]", err)
				}
			} else {
				logger.PduSessLog.Traceln("Send SMContext Status Notification successfully")
			}
		}
		p.removeSMContextFromAllNF(smContext, false)

	case smf_context.SessionReleaseFailed:
		smContext.Log.Traceln("In case SessionReleaseFailed")
		smContext.SetState(smf_context.Active)

	default:
		smContext.Log.Warnf("The state shouldn't be [%s]", pfcpResponseStatus)
		logger.CtxLog.Traceln("In case Unknown")
		smContext.SetState(smf_context.Active)
	}
}

func (p *Processor) makeEstRejectResAndReleaseSMContext(
	c *gin.Context,
	smContext *smf_context.SMContext,
	nasErrorCause uint8,
	sbiError *models.ProblemDetails,
) {
	if buf, err := smf_context.
		BuildGSMPDUSessionEstablishmentReject(
			smContext,
			nasErrorCause); err != nil {
		postSmContextsError := models.PostSmContextsErrorResponse{
			JsonData: &models.SmContextCreateError{
				Error:   sbiError,
				N1SmMsg: &models.RefToBinaryData{ContentId: "n1SmMsg"},
			},
		}
		p.nasErrorResponse(c, int(sbiError.Status), postSmContextsError)
	} else {
		postSmContextsError := models.PostSmContextsErrorResponse{
			JsonData: &models.SmContextCreateError{
				Error:   sbiError,
				N1SmMsg: &models.RefToBinaryData{ContentId: "n1SmMsg"},
			},
			BinaryDataN1SmMessage: buf,
		}
		p.nasErrorResponse(c, int(sbiError.Status), postSmContextsError)
	}
	p.removeSMContextFromAllNF(smContext, false)
}

func (p *Processor) sendPDUSessionEstablishmentReject(
	smContext *smf_context.SMContext,
	nasErrorCause uint8,
) {
	smNasBuf, err := smf_context.BuildGSMPDUSessionEstablishmentReject(
		smContext, nasMessage.Cause5GSMNetworkFailure)
	if err != nil {
		logger.PduSessLog.Errorf("Build GSM PDUSessionEstablishmentReject failed: %s", err)
		return
	}

	n1n2Request := models.N1N2MessageTransferRequest{
		BinaryDataN1Message: smNasBuf,
		JsonData: &models.N1N2MessageTransferReqData{
			PduSessionId: smContext.PDUSessionID,
			N1MessageContainer: &models.N1MessageContainer{
				N1MessageClass:   "SM",
				N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
			},
		},
	}

	smContext.SetState(smf_context.InActive)

	ctx, _, errToken := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NAMF_COMM, models.NfType_AMF)
	if errToken != nil {
		logger.PduSessLog.Warnf("Get NAMF_COMM context failed: %s", errToken)
		return
	}
	rspData, _, err := p.Consumer().
		N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request, smContext.CommunicationClientApiPrefix)
	if err != nil {
		logger.ConsumerLog.Warnf("N1N2MessageTransfer for SendPDUSessionEstablishmentReject failed: %+v", err)
		return
	}

	if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
		logger.PduSessLog.Warnf("%v", rspData.Cause)
	}
	p.removeSMContextFromAllNF(smContext, true)
}

func (p *Processor) sendPDUSessionEstablishmentAccept(
	smContext *smf_context.SMContext,
) {
	smNasBuf, err := smf_context.BuildGSMPDUSessionEstablishmentAccept(smContext)
	if err != nil {
		logger.PduSessLog.Errorf("Build GSM PDUSessionEstablishmentAccept failed: %s", err)
		return
	}

	n2Pdu, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext)
	if err != nil {
		logger.PduSessLog.Errorf("Build PDUSessionResourceSetupRequestTransfer failed: %s", err)
		return
	}

	n1n2Request := models.N1N2MessageTransferRequest{
		BinaryDataN1Message:     smNasBuf,
		BinaryDataN2Information: n2Pdu,
		JsonData: &models.N1N2MessageTransferReqData{
			PduSessionId: smContext.PDUSessionID,
			N1MessageContainer: &models.N1MessageContainer{
				N1MessageClass:   "SM",
				N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
			},
			N2InfoContainer: &models.N2InfoContainer{
				N2InformationClass: models.N2InformationClass_SM,
				SmInfo: &models.N2SmInformation{
					PduSessionId: smContext.PDUSessionID,
					N2InfoContent: &models.N2InfoContent{
						NgapIeType: models.NgapIeType_PDU_RES_SETUP_REQ,
						NgapData: &models.RefToBinaryData{
							ContentId: "N2SmInformation",
						},
					},
					SNssai: smContext.SNssai,
				},
			},
		},
	}

	ctx, _, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NAMF_COMM, models.NfType_AMF)
	if err != nil {
		logger.PduSessLog.Warnf("Get NAMF_COMM context failed: %s", err)
		return
	}

	rspData, _, err := p.Consumer().
		N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request, smContext.CommunicationClientApiPrefix)
	if err != nil {
		logger.ConsumerLog.Warnf("N1N2MessageTransfer for sendPDUSessionEstablishmentAccept failed: %+v", err)
		return
	}

	smContext.SetState(smf_context.Active)

	if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
		logger.PduSessLog.Warnf("%v", rspData.Cause)
	}
}

func (p *Processor) sendGSMPDUSessionReleaseCommand(smContext *smf_context.SMContext, nasPdu []byte) {
	n1n2Request := models.N1N2MessageTransferRequest{}
	n1n2Request.JsonData = &models.N1N2MessageTransferReqData{
		PduSessionId: smContext.PDUSessionID,
		N1MessageContainer: &models.N1MessageContainer{
			N1MessageClass:   "SM",
			N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
		},
	}
	n1n2Request.BinaryDataN1Message = nasPdu
	if smContext.T3592 != nil {
		smContext.T3592.Stop()
		smContext.T3592 = nil
	}

	// Start T3592
	t3592 := factory.SmfConfig.Configuration.T3592
	if t3592.Enable {
		ctx, _, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NAMF_COMM, models.NfType_AMF)
		if err != nil {
			smContext.Log.Warnf("Get namf-comm token failed: %+v", err)
			return
		}

		smContext.T3592 = smf_context.NewTimer(t3592.ExpireTime, t3592.MaxRetryTimes, func(expireTimes int32) {
			rspData, _, errMsgTransfer := p.Consumer().
				N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request, smContext.CommunicationClientApiPrefix)
			if errMsgTransfer != nil {
				logger.ConsumerLog.Warnf("N1N2MessageTransfer for GSMPDUSessionReleaseCommand failed: %+v", errMsgTransfer)
				return
			}

			if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
				smContext.Log.Warnf("%v", rspData.Cause)
			}
		}, func() {
			smContext.Log.Warn("T3592 Expires 3 times, abort notification procedure")
			smContext.T3592 = nil
			p.sendReleaseNotification(smContext)
		})
	}
}

func (p *Processor) sendGSMPDUSessionModificationCommand(smContext *smf_context.SMContext, nasPdu []byte) {
	n1n2Request := models.N1N2MessageTransferRequest{}
	n1n2Request.JsonData = &models.N1N2MessageTransferReqData{
		PduSessionId: smContext.PDUSessionID,
		N1MessageContainer: &models.N1MessageContainer{
			N1MessageClass:   "SM",
			N1MessageContent: &models.RefToBinaryData{ContentId: "GSM_NAS"},
		},
	}
	n1n2Request.BinaryDataN1Message = nasPdu

	if smContext.T3591 != nil {
		smContext.T3591.Stop()
		smContext.T3591 = nil
	}

	// Start T3591
	t3591 := factory.SmfConfig.Configuration.T3591
	if t3591.Enable {
		ctx, _, err := smf_context.GetSelf().GetTokenCtx(models.ServiceName_NAMF_COMM, models.NfType_AMF)
		if err != nil {
			smContext.Log.Warnf("Get namf-comm token failed: %+v", err)
			return
		}

		smContext.T3591 = smf_context.NewTimer(t3591.ExpireTime, t3591.MaxRetryTimes, func(expireTimes int32) {
			rspData, _, errMsgTransfer := p.Consumer().
				N1N2MessageTransfer(ctx, smContext.Supi, n1n2Request, smContext.CommunicationClientApiPrefix)
			if errMsgTransfer != nil {
				logger.ConsumerLog.Warnf("N1N2MessageTransfer for GSMPDUSessionModificationCommand failed: %+v", errMsgTransfer)
				return
			}

			if rspData.Cause == models.N1N2MessageTransferCause_N1_MSG_NOT_TRANSFERRED {
				smContext.Log.Warnf("%v", rspData.Cause)
			}
		}, func() {
			smContext.Log.Warn("T3591 Expires3 times, abort notification procedure")
			smContext.T3591 = nil
		})
	}
}

func (p *Processor) nasErrorResponse(
	c *gin.Context,
	status int,
	errBody models.PostSmContextsErrorResponse,
) {
	switch status {
	case http.StatusForbidden,
		// http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusGatewayTimeout:
		c.Render(status, openapi.MultipartRelatedRender{Data: errBody})
	default:
		c.JSON(status, errBody)
	}
}

func AuthDefQosToString(a *models.AuthorizedDefaultQos) string {
	str := "AuthorizedDefaultQos: {"
	str += fmt.Sprintf("Var5qi: %d ", a.Var5qi)
	str += fmt.Sprintf("Arp: %+v ", a.Arp)
	str += fmt.Sprintf("PriorityLevel: %d ", a.PriorityLevel)
	str += "} "
	return str
}

func SessRuleToString(rule *models.SessionRule, prefix string) string {
	str := prefix + "SessionRule {"
	str += fmt.Sprintf("AuthSessAmbr: { %+v } ", rule.AuthSessAmbr)
	str += fmt.Sprint(AuthDefQosToString(rule.AuthDefQos))
	str += fmt.Sprintf("SessRuleId: %s ", rule.SessRuleId)
	str += fmt.Sprintf("RefUmData: %s ", rule.RefUmData)
	str += fmt.Sprintf("RefCondData: %s ", rule.RefCondData)
	str += prefix + "}"
	return str
}

func SmPolicyDecisionToString(decision *models.SmPolicyDecision) string {
	str := "SmPolicyDecision {\n"
	prefix := "  "
	str += prefix + fmt.Sprintf("SessRules %+v\n", decision.SessRules)
	for _, rule := range decision.SessRules {
		str += prefix + fmt.Sprintln(SessRuleToString(rule, prefix))
	}
	str += prefix + fmt.Sprintf("PccRules %+v\n", decision.PccRules)
	for _, pccRule := range decision.PccRules {
		str += prefix + fmt.Sprintf("PccRule %+v\n", pccRule)
	}
	str += prefix + fmt.Sprintf("QosDecs %+v\n", decision.QosDecs)
	for _, qos := range decision.QosDecs {
		str += prefix + fmt.Sprintf("QosDec %+v\n", qos)
	}
	str += "}"
	return str
}

func PDUSessionEstablishmentRequestToString(r *nasMessage.PDUSessionEstablishmentRequest) string {
	str := "PDUSessionEstablishmentRequest: {\n"
	prefix := "  "
	str += prefix + fmt.Sprintf("ExtendedProtocolDiscriminator: %d\n", r.ExtendedProtocolDiscriminator)
	str += prefix + fmt.Sprintf("PDUSessionID: %d\n", r.PDUSessionID)
	str += prefix + fmt.Sprintf("PTI: %d\n", r.PTI)
	str += prefix + fmt.Sprintf("PDUSESSIONESTABLISHMENTREQUESTMessageIdentity: %d\n", r.PDUSESSIONESTABLISHMENTREQUESTMessageIdentity)
	str += prefix + fmt.Sprintf("IntegrityProtectionMaximumDataRate: %+v\n", r.IntegrityProtectionMaximumDataRate)
	str += prefix + fmt.Sprintf("PDUSessionType: %+v\n", r.PDUSessionType)
	str += prefix + fmt.Sprintf("SSCMode: %+v\n", r.SSCMode)
	str += prefix + fmt.Sprintf("Capability5GSM: %+v\n", r.Capability5GSM)
	str += prefix + fmt.Sprintf("ExtendedProtocolConfigurationOptions: %+v\n", r.ExtendedProtocolConfigurationOptions)
	str += "}"
	return str
}
