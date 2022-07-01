package producer

import (
	"context"
	"net"
	"net/http"

	"github.com/antihax/optional"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/Namf_Communication"
	"github.com/free5gc/openapi/Nsmf_PDUSession"
	"github.com/free5gc/openapi/Nudm_SubscriberDataManagement"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/pfcp/pfcpType"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/internal/sbi/consumer"
	"github.com/free5gc/util/httpwrapper"
)

func HandlePDUSessionSMContextCreate(request models.PostSmContextsRequest) *httpwrapper.Response {
	// GSM State
	// PDU Session Establishment Accept/Reject
	var response models.PostSmContextsResponse
	response.JsonData = new(models.SmContextCreatedData)
	logger.PduSessLog.Infoln("In HandlePDUSessionSMContextCreate")

	// Check has PDU Session Establishment Request
	m := nas.NewMessage()
	if err := m.GsmMessageDecode(&request.BinaryDataN1SmMessage); err != nil ||
		m.GsmHeader.GetMessageType() != nas.MsgTypePDUSessionEstablishmentRequest {
		logger.PduSessLog.Warnln("GsmMessageDecode Error: ", err)
		httpResponse := &httpwrapper.Response{
			Header: nil,
			Status: http.StatusForbidden,
			Body: models.PostSmContextsErrorResponse{
				JsonData: &models.SmContextCreateError{
					Error: &Nsmf_PDUSession.N1SmError,
				},
			},
		}
		return httpResponse
	}

	// Check duplicate SM Context
	createData := request.JsonData
	if dup_smCtx := smf_context.GetSMContextById(createData.Supi, createData.PduSessionId); dup_smCtx != nil {
		HandlePDUSessionSMContextLocalRelease(dup_smCtx, createData)
	}

	smContext := smf_context.NewSMContext(createData.Supi, createData.PduSessionId)
	smContext.SMContextState = smf_context.ActivePending
	logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
	smContext.SetCreateData(createData)
	smContext.SmStatusNotifyUri = createData.SmContextStatusUri

	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	// DNN Information from config
	smContext.DNNInfo = smf_context.RetrieveDnnInformation(createData.SNssai, createData.Dnn)
	if smContext.DNNInfo == nil {
		logger.PduSessLog.Errorf("S-NSSAI[sst: %d, sd: %s] DNN[%s] not matched DNN Config",
			createData.SNssai.Sst, createData.SNssai.Sd, createData.Dnn)
	}

	// Query UDM
	if problemDetails, err := consumer.SendNFDiscoveryUDM(); err != nil {
		logger.PduSessLog.Warnf("Send NF Discovery Serving UDM Error[%v]", err)
	} else if problemDetails != nil {
		logger.PduSessLog.Warnf("Send NF Discovery Serving UDM Problem[%+v]", problemDetails)
	} else {
		logger.PduSessLog.Infoln("Send NF Discovery Serving UDM Successfully")
	}

	// IP Allocation
	upfSelectionParams := &smf_context.UPFSelectionParams{
		Dnn: createData.Dnn,
		SNssai: &smf_context.SNssai{
			Sst: createData.SNssai.Sst,
			Sd:  createData.SNssai.Sd,
		},
	}
	var selectedUPF *smf_context.UPNode
	var ip net.IP
	selectedUPFName := ""
	if smf_context.SMF_Self().ULCLSupport && smf_context.CheckUEHasPreConfig(createData.Supi) {
		groupName := smf_context.GetULCLGroupNameFromSUPI(createData.Supi)
		defaultPathPool := smf_context.GetUEDefaultPathPool(groupName)
		if defaultPathPool != nil {
			selectedUPFName, ip = defaultPathPool.SelectUPFAndAllocUEIPForULCL(
				smf_context.GetUserPlaneInformation(), upfSelectionParams)
			selectedUPF = smf_context.GetUserPlaneInformation().UPFs[selectedUPFName]
		}
	} else {
		selectedUPF, ip = smf_context.GetUserPlaneInformation().SelectUPFAndAllocUEIP(upfSelectionParams)
		smContext.PDUAddress = ip
		logger.PduSessLog.Infof("UE[%s] PDUSessionID[%d] IP[%s]",
			smContext.Supi, smContext.PDUSessionID, smContext.PDUAddress.String())
	}
	if ip == nil && (selectedUPF == nil || selectedUPFName == "") {
		logger.PduSessLog.Error("failed allocate IP address for this SM")

		smContext.SMContextState = smf_context.InActive
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		logger.PduSessLog.Warnf("Data Path not found\n")
		logger.PduSessLog.Warnln("Selection Parameter: ", upfSelectionParams.String())

		return makeErrorResponse(smContext, nasMessage.Cause5GSMInsufficientResourcesForSpecificSliceAndDNN,
			&Nsmf_PDUSession.InsufficientResourceSliceDnn)
	}
	smContext.PDUAddress = ip
	smContext.SelectedUPF = selectedUPF

	smPlmnID := createData.Guami.PlmnId

	smDataParams := &Nudm_SubscriberDataManagement.GetSmDataParamOpts{
		Dnn:         optional.NewString(createData.Dnn),
		PlmnId:      optional.NewInterface(openapi.MarshToJsonString(smPlmnID)),
		SingleNssai: optional.NewInterface(openapi.MarshToJsonString(smContext.Snssai)),
	}

	SubscriberDataManagementClient := smf_context.SMF_Self().SubscriberDataManagementClient

	if sessSubData, rsp, err := SubscriberDataManagementClient.
		SessionManagementSubscriptionDataRetrievalApi.
		GetSmData(context.Background(), smContext.Supi, smDataParams); err != nil {
		logger.PduSessLog.Errorln("Get SessionManagementSubscriptionData error:", err)
	} else {
		defer func() {
			if rspCloseErr := rsp.Body.Close(); rspCloseErr != nil {
				logger.PduSessLog.Errorf("GetSmData response body cannot close: %+v", rspCloseErr)
			}
		}()
		if len(sessSubData) > 0 {
			smContext.DnnConfiguration = sessSubData[0].DnnConfigurations[smContext.Dnn]
			// UP Security info present in session management subscription data
			if smContext.DnnConfiguration.UpSecurity != nil {
				smContext.UpSecurity = smContext.DnnConfiguration.UpSecurity
			}
		} else {
			logger.PduSessLog.Errorln("SessionManagementSubscriptionData from UDM is nil")
		}
	}

	establishmentRequest := m.PDUSessionEstablishmentRequest
	smContext.HandlePDUSessionEstablishmentRequest(establishmentRequest)

	logger.PduSessLog.Infof("PCF Selection for SMContext SUPI[%s] PDUSessionID[%d]\n",
		smContext.Supi, smContext.PDUSessionID)
	if err := smContext.PCFSelection(); err != nil {
		logger.PduSessLog.Errorln("pcf selection error:", err)
	}

	var smPolicyDecision *models.SmPolicyDecision
	if smPolicyID, smPolicyDecisionRsp, err := consumer.SendSMPolicyAssociationCreate(smContext); err != nil {
		openapiError := err.(openapi.GenericOpenAPIError)
		problemDetails := openapiError.Model().(models.ProblemDetails)
		logger.PduSessLog.Errorln("setup sm policy association failed:", err, problemDetails)
		smContext.SMContextState = smf_context.InActive
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())

		if problemDetails.Cause == "USER_UNKNOWN" {
			return makeErrorResponse(smContext, nasMessage.Cause5GSMRequestRejectedUnspecified,
				&Nsmf_PDUSession.SubscriptionDenied)
		} else {
			return makeErrorResponse(smContext, nasMessage.Cause5GSMNetworkFailure, &Nsmf_PDUSession.NetworkFailure)
		}
	} else {
		smPolicyDecision = smPolicyDecisionRsp
		smContext.SMPolicyID = smPolicyID
	}

	// dataPath selection
	smContext.Tunnel = smf_context.NewUPTunnel()
	if err := ApplySmPolicyFromDecision(smContext, smPolicyDecision); err != nil {
		logger.PduSessLog.Errorf("apply sm policy decision error: %+v", err)
	}
	var defaultPath *smf_context.DataPath

	if smf_context.SMF_Self().ULCLSupport && smf_context.CheckUEHasPreConfig(createData.Supi) {
		logger.PduSessLog.Infof("SUPI[%s] has pre-config route", createData.Supi)
		uePreConfigPaths := smf_context.GetUEPreConfigPaths(createData.Supi, selectedUPFName)
		smContext.Tunnel.DataPathPool = uePreConfigPaths.DataPathPool
		smContext.Tunnel.PathIDGenerator = uePreConfigPaths.PathIDGenerator
		defaultPath = smContext.Tunnel.DataPathPool.GetDefaultPath()
		defaultPath.ActivateTunnelAndPDR(smContext, 255)
		smContext.BPManager = smf_context.NewBPManager(createData.Supi)
	} else {
		// UE has no pre-config path.
		// Use default route
		logger.PduSessLog.Infof("SUPI[%s] has no pre-config route", createData.Supi)
		defaultUPPath := smf_context.GetUserPlaneInformation().GetDefaultUserPlanePathByDNNAndUPF(
			upfSelectionParams, smContext.SelectedUPF)
		defaultPath = smf_context.GenerateDataPath(defaultUPPath, smContext)
		if defaultPath != nil {
			defaultPath.IsDefaultPath = true
			smContext.Tunnel.AddDataPath(defaultPath)
			defaultPath.ActivateTunnelAndPDR(smContext, 255)
		}
	}

	if defaultPath == nil {
		smContext.SMContextState = smf_context.InActive
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		logger.PduSessLog.Warnf("Data Path not found\n")
		logger.PduSessLog.Warnln("Selection Parameter: ", upfSelectionParams.String())

		return makeErrorResponse(smContext, nasMessage.Cause5GSMInsufficientResourcesForSpecificSliceAndDNN,
			&Nsmf_PDUSession.InsufficientResourceSliceDnn)
	}

	if problemDetails, err := consumer.SendNFDiscoveryServingAMF(smContext); err != nil {
		logger.PduSessLog.Warnf("Send NF Discovery Serving AMF Error[%v]", err)
	} else if problemDetails != nil {
		logger.PduSessLog.Warnf("Send NF Discovery Serving AMF Problem[%+v]", problemDetails)
	} else {
		logger.PduSessLog.Traceln("Send NF Discovery Serving AMF successfully")
	}

	for _, service := range *smContext.AMFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NAMF_COMM {
			communicationConf := Namf_Communication.NewConfiguration()
			communicationConf.SetBasePath(service.ApiPrefix)
			smContext.CommunicationClient = Namf_Communication.NewAPIClient(communicationConf)
		}
	}
	go ActivateUPFSessionAndNotifyUE(smContext)

	response.JsonData = smContext.BuildCreatedData()
	httpResponse := &httpwrapper.Response{
		Header: http.Header{
			"Location": {smContext.Ref},
		},
		Status: http.StatusCreated,
		Body:   response,
	}

	return httpResponse
	// TODO: UECM registration
}

func HandlePDUSessionSMContextUpdate(smContextRef string, body models.UpdateSmContextRequest) *httpwrapper.Response {
	// GSM State
	// PDU Session Modification Reject(Cause Value == 43 || Cause Value != 43)/Complete
	// PDU Session Release Command/Complete
	logger.PduSessLog.Infoln("In HandlePDUSessionSMContextUpdate")
	smContext := smf_context.GetSMContextByRef(smContextRef)

	if smContext == nil {
		logger.PduSessLog.Warnf("SMContext[%s] is not found", smContextRef)

		httpResponse := &httpwrapper.Response{
			Header: nil,
			Status: http.StatusNotFound,
			Body: models.UpdateSmContextErrorResponse{
				JsonData: &models.SmContextUpdateError{
					UpCnxState: models.UpCnxState_DEACTIVATED,
					Error: &models.ProblemDetails{
						Type:   "Resource Not Found",
						Title:  "SMContext Ref is not found",
						Status: http.StatusNotFound,
					},
				},
			},
		}
		return httpResponse
	}

	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	var sendPFCPModification bool
	var pfcpResponseStatus smf_context.PFCPSessionResponseStatus
	var response models.UpdateSmContextResponse
	response.JsonData = new(models.SmContextUpdatedData)

	smContextUpdateData := body.JsonData

	if body.BinaryDataN1SmMessage != nil {
		logger.PduSessLog.Traceln("Binary Data N1 SmMessage isn't nil!")
		m := nas.NewMessage()
		err := m.GsmMessageDecode(&body.BinaryDataN1SmMessage)
		logger.PduSessLog.Traceln("[SMF] UpdateSmContextRequest N1SmMessage: ", m)
		if err != nil {
			logger.PduSessLog.Error(err)
			httpResponse := &httpwrapper.Response{
				Status: http.StatusForbidden,
				Body: models.UpdateSmContextErrorResponse{
					JsonData: &models.SmContextUpdateError{
						Error: &Nsmf_PDUSession.N1SmError,
					},
				}, // Depends on the reason why N4 fail
			}
			return httpResponse
		}
		switch m.GsmHeader.GetMessageType() {
		case nas.MsgTypePDUSessionReleaseRequest:
			state := smContext.SMContextState
			if !(state == smf_context.Active || state == smf_context.InActivePending) {
				logger.PduSessLog.Warnln("The SMContext State should be Active or InActivePending State")
				logger.PduSessLog.Warnln("SMContext state: ", state.String())
				return &httpwrapper.Response{
					Status: http.StatusForbidden,
					Body: models.UpdateSmContextErrorResponse{
						JsonData: &models.SmContextUpdateError{
							Error: &Nsmf_PDUSession.N1SmError,
						},
					},
				}
			}

			smContext.HandlePDUSessionReleaseRequest(m.PDUSessionReleaseRequest)
			if smContext.SelectedUPF != nil {
				logger.PduSessLog.Infof("UE[%s] PDUSessionID[%d] Release IP[%s]",
					smContext.Supi, smContext.PDUSessionID, smContext.PDUAddress.String())
				smf_context.GetUserPlaneInformation().ReleaseUEIP(smContext.SelectedUPF, smContext.PDUAddress)
			}

			// remove SM Policy Association
			if smContext.SMPolicyID != "" {
				if err := consumer.SendSMPolicyAssociationTermination(smContext); err != nil {
					logger.PduSessLog.Errorf("SM Policy Termination failed: %s", err)
				} else {
					smContext.SMPolicyID = ""
				}
			}

			cause := nasMessage.Cause5GSMRegularDeactivation
			if m.PDUSessionReleaseRequest.Cause5GSM != nil {
				cause = m.PDUSessionReleaseRequest.Cause5GSM.GetCauseValue()
			}

			if buf, err := smf_context.BuildGSMPDUSessionReleaseCommand(smContext, cause); err != nil {
				logger.PduSessLog.Errorf("Build GSM PDUSessionReleaseCommand failed: %+v", err)
			} else {
				response.BinaryDataN1SmMessage = buf
				response.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseCommand"}
			}

			if buf, err := smf_context.BuildPDUSessionResourceReleaseCommandTransfer(smContext); err != nil {
				logger.PduSessLog.Errorf("Build PDUSessionResourceReleaseCommandTransfer failed: %+v", err)
			} else {
				response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_REL_CMD
				response.BinaryDataN2SmInformation = buf
				response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUResourceReleaseCommand"}
			}

			// In the case of dupulicated PDUSessionReleaseRequest, skip deleting PFCP sessions
			if smContext.SMContextState == smf_context.InActivePending {
				logger.CtxLog.Infof("Skip deleting the PFCP sessions of PDUSessionID:%d of SUPI:%s",
					smContext.PDUSessionID, smContext.Supi)
				return &httpwrapper.Response{
					Status: http.StatusOK,
					Body:   response,
				}
			}

			smContext.SMContextState = smf_context.PFCPModification
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())

			pfcpResponseStatus = releaseSession(smContext)
		case nas.MsgTypePDUSessionReleaseComplete:
			if smContext.SMContextState != smf_context.InActivePending {
				logger.PduSessLog.Warnln("The SMContext State should be InActivePending State")
				logger.PduSessLog.Warnln("SMContext state: ", smContext.SMContextState.String())
				return &httpwrapper.Response{
					Status: http.StatusForbidden,
					Body: models.UpdateSmContextErrorResponse{
						JsonData: &models.SmContextUpdateError{
							Error: &Nsmf_PDUSession.N1SmError,
						},
					},
				}
			}
			// Send Release Notify to AMF
			logger.PduSessLog.Infoln("[SMF] Send Update SmContext Response")
			smContext.SMContextState = smf_context.InActive
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
			problemDetails, err := consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
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
	} else {
		logger.PduSessLog.Traceln("[SMF] Binary Data N1 SmMessage is nil!")
	}

	tunnel := smContext.Tunnel
	pdrList := []*smf_context.PDR{}
	farList := []*smf_context.FAR{}
	barList := []*smf_context.BAR{}
	qerList := []*smf_context.QER{}

	switch smContextUpdateData.UpCnxState {
	case models.UpCnxState_ACTIVATING:
		if smContext.SMContextState != smf_context.Active {
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
			httpResponse := &httpwrapper.Response{
				Status: http.StatusForbidden,
				Body: models.UpdateSmContextErrorResponse{
					JsonData: &models.SmContextUpdateError{
						UpCnxState: models.UpCnxState_DEACTIVATED,
						Error:      &Nsmf_PDUSession.SmContextStateMismatchActive,
					},
				},
			}
			return httpResponse
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUSessionResourceSetupRequestTransfer"}
		response.JsonData.UpCnxState = models.UpCnxState_ACTIVATING
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ

		n2Buf, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext)
		if err != nil {
			logger.PduSessLog.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		} else {
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUSessionResourceSetupRequestTransfer"}
		}
		smContext.UpCnxState = models.UpCnxState_ACTIVATING
	case models.UpCnxState_DEACTIVATED:
		state := smContext.SMContextState
		// If the PDU session has been released, skip sending PFCP Session Modification Request
		if state == smf_context.InActivePending || state == smf_context.InActive {
			logger.CtxLog.Infof("Skip sending PFCP Session Modification Request of PDUSessionID:%d of SUPI:%s",
				smContext.PDUSessionID, smContext.Supi)
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
			return &httpwrapper.Response{
				Status: http.StatusOK,
				Body:   response,
			}
		} else if state != smf_context.Active {
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
			httpResponse := &httpwrapper.Response{
				Status: http.StatusForbidden,
				Body: models.UpdateSmContextErrorResponse{
					JsonData: &models.SmContextUpdateError{
						UpCnxState: models.UpCnxState_DEACTIVATED,
						Error:      &Nsmf_PDUSession.SmContextStateMismatchActive,
					},
				},
			}
			return httpResponse
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
		smContext.UpCnxState = body.JsonData.UpCnxState
		smContext.UeLocation = body.JsonData.UeLocation
		// TODO: Deactivate N2 downlink tunnel
		// Set FAR and An, N3 Release Info
		farList = []*smf_context.FAR{}
		smContext.PendingUPF = make(smf_context.PendingUPF)
		for _, dataPath := range smContext.Tunnel.DataPathPool {
			ANUPF := dataPath.FirstDPNode
			DLPDR := ANUPF.DownLinkTunnel.PDR
			if DLPDR == nil {
				logger.PduSessLog.Errorf("AN Release Error")
			} else {
				DLPDR.FAR.State = smf_context.RULE_UPDATE
				DLPDR.FAR.ApplyAction.Forw = false
				DLPDR.FAR.ApplyAction.Buff = true
				DLPDR.FAR.ApplyAction.Nocp = true
				smContext.PendingUPF[ANUPF.GetNodeIP()] = true
				farList = append(farList, DLPDR.FAR)
				sendPFCPModification = true
				smContext.SMContextState = smf_context.PFCPModification
			}
		}

		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
	}

	switch smContextUpdateData.N2SmInfoType {
	case models.N2SmInfoType_PDU_RES_SETUP_RSP:
		if smContext.SMContextState != smf_context.Active {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
			return &httpwrapper.Response{
				Status: http.StatusForbidden,
				Body: models.UpdateSmContextErrorResponse{
					JsonData: &models.SmContextUpdateError{
						Error: &Nsmf_PDUSession.N2SmError,
					},
				},
			}
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		pdrList = []*smf_context.PDR{}
		farList = []*smf_context.FAR{}

		smContext.PendingUPF = make(smf_context.PendingUPF)
		for _, dataPath := range tunnel.DataPathPool {
			if dataPath.Activated {
				ANUPF := dataPath.FirstDPNode
				DLPDR := ANUPF.DownLinkTunnel.PDR

				DLPDR.FAR.ApplyAction = pfcpType.ApplyAction{Buff: false, Drop: false, Dupl: false, Forw: true, Nocp: false}
				DLPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
					DestinationInterface: pfcpType.DestinationInterface{
						InterfaceValue: pfcpType.DestinationInterfaceAccess,
					},
					NetworkInstance: &pfcpType.NetworkInstance{NetworkInstance: smContext.Dnn},
				}

				DLPDR.State = smf_context.RULE_UPDATE
				DLPDR.FAR.State = smf_context.RULE_UPDATE

				pdrList = append(pdrList, DLPDR)
				farList = append(farList, DLPDR.FAR)

				if _, exist := smContext.PendingUPF[ANUPF.GetNodeIP()]; !exist {
					smContext.PendingUPF[ANUPF.GetNodeIP()] = true
				}
			}
		}

		if err := smf_context.
			HandlePDUSessionResourceSetupResponseTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			logger.PduSessLog.Errorf("Handle PDUSessionResourceSetupResponseTransfer failed: %+v", err)
		}
		sendPFCPModification = true
		smContext.SMContextState = smf_context.PFCPModification
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
	case models.N2SmInfoType_PDU_RES_SETUP_FAIL:
		if err := smf_context.
			HandlePDUSessionResourceSetupResponseTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			logger.PduSessLog.Errorf("Handle PDUSessionResourceSetupResponseTransfer failed: %+v", err)
		}
	case models.N2SmInfoType_PDU_RES_REL_RSP:
		logger.PduSessLog.Infoln("[SMF] N2 PDUSession Release Complete ")
		if smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID {
			state := smContext.SMContextState
			if !(state == smf_context.InActivePending || state == smf_context.InActive) {
				logger.PduSessLog.Warnf("SMContext[%s-%02d] should be InActivePending, but actual %s",
					smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
				return &httpwrapper.Response{
					Status: http.StatusForbidden,
					Body: models.UpdateSmContextErrorResponse{
						JsonData: &models.SmContextUpdateError{
							Error: &Nsmf_PDUSession.N2SmError,
						},
					},
				}
			}
			smContext.SMContextState = smf_context.InActive
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			logger.PduSessLog.Infoln("[SMF] Send Update SmContext Response")
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED

			smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID = false
			smf_context.RemoveSMContext(smContext.Ref)
			problemDetails, err := consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
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
		} else { // normal case
			if smContext.SMContextState != smf_context.InActivePending {
				logger.PduSessLog.Warnf("SMContext[%s-%02d] should be InActivePending, but actual %s",
					smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
				return &httpwrapper.Response{
					Status: http.StatusForbidden,
					Body: models.UpdateSmContextErrorResponse{
						JsonData: &models.SmContextUpdateError{
							Error: &Nsmf_PDUSession.N2SmError,
						},
					},
				}
			}
			logger.PduSessLog.Infoln("[SMF] Send Update SmContext Response")
		}
	case models.N2SmInfoType_PATH_SWITCH_REQ:
		logger.PduSessLog.Traceln("Handle Path Switch Request")
		if smContext.SMContextState != smf_context.Active {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())

		if err := smf_context.HandlePathSwitchRequestTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			logger.PduSessLog.Errorf("Handle PathSwitchRequestTransfer: %+v", err)
		}

		if n2Buf, err := smf_context.BuildPathSwitchRequestAcknowledgeTransfer(smContext); err != nil {
			logger.PduSessLog.Errorf("Build Path Switch Transfer Error(%+v)", err)
		} else {
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PATH_SWITCH_REQ_ACK
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfo = &models.RefToBinaryData{
				ContentId: "PATH_SWITCH_REQ_ACK",
			}
		}

		smContext.PendingUPF = make(smf_context.PendingUPF)
		for _, dataPath := range tunnel.DataPathPool {
			if dataPath.Activated {
				ANUPF := dataPath.FirstDPNode
				DLPDR := ANUPF.DownLinkTunnel.PDR

				pdrList = append(pdrList, DLPDR)
				farList = append(farList, DLPDR.FAR)

				if _, exist := smContext.PendingUPF[ANUPF.GetNodeIP()]; !exist {
					smContext.PendingUPF[ANUPF.GetNodeIP()] = true
				}
			}
		}

		sendPFCPModification = true
		smContext.SMContextState = smf_context.PFCPModification
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
	case models.N2SmInfoType_PATH_SWITCH_SETUP_FAIL:
		if smContext.SMContextState != smf_context.Active {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		if err := smf_context.HandlePathSwitchRequestSetupFailedTransfer(
			body.BinaryDataN2SmInformation, smContext); err != nil {
			logger.PduSessLog.Error()
		}
	case models.N2SmInfoType_HANDOVER_REQUIRED:
		if smContext.SMContextState != smf_context.Active {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "Handover"}
	}

	switch smContextUpdateData.HoState {
	case models.HoState_PREPARING:
		logger.PduSessLog.Traceln("In HoState_PREPARING")
		if smContext.SMContextState != smf_context.Active {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		smContext.HoState = models.HoState_PREPARING
		if err := smf_context.HandleHandoverRequiredTransfer(body.BinaryDataN2SmInformation, smContext); err != nil {
			logger.PduSessLog.Errorf("Handle HandoverRequiredTransfer failed: %+v", err)
		}
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ

		if n2Buf, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext); err != nil {
			logger.PduSessLog.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		} else {
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ
			response.JsonData.N2SmInfo = &models.RefToBinaryData{
				ContentId: "PDU_RES_SETUP_REQ",
			}
		}
		response.JsonData.HoState = models.HoState_PREPARING
	case models.HoState_PREPARED:
		logger.PduSessLog.Traceln("In HoState_PREPARED")
		if smContext.SMContextState != smf_context.Active {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		smContext.HoState = models.HoState_PREPARED
		response.JsonData.HoState = models.HoState_PREPARED
		if err := smf_context.HandleHandoverRequestAcknowledgeTransfer(
			body.BinaryDataN2SmInformation, smContext); err != nil {
			logger.PduSessLog.Errorf("Handle HandoverRequestAcknowledgeTransfer failed: %+v", err)
		}

		if n2Buf, err := smf_context.BuildHandoverCommandTransfer(smContext); err != nil {
			logger.PduSessLog.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		} else {
			response.BinaryDataN2SmInformation = n2Buf
			response.JsonData.N2SmInfoType = models.N2SmInfoType_HANDOVER_CMD
			response.JsonData.N2SmInfo = &models.RefToBinaryData{
				ContentId: "HANDOVER_CMD",
			}
		}
		response.JsonData.HoState = models.HoState_PREPARING
	case models.HoState_COMPLETED:
		logger.PduSessLog.Traceln("In HoState_COMPLETED")
		if smContext.SMContextState != smf_context.Active {
			// Wait till the state becomes Active again
			// TODO: implement sleep wait in concurrent architecture
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
		}
		smContext.SMContextState = smf_context.ModificationPending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		smContext.HoState = models.HoState_COMPLETED
		response.JsonData.HoState = models.HoState_COMPLETED
	}

	switch smContextUpdateData.Cause {
	case models.Cause_REL_DUE_TO_DUPLICATE_SESSION_ID:
		buf, err := smf_context.BuildPDUSessionResourceReleaseCommandTransfer(smContext)
		if err != nil {
			logger.PduSessLog.Error(err)
		} else {
			response.BinaryDataN2SmInformation = buf
			response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUResourceReleaseCommand"}
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_REL_CMD
		}

		//* release PDU Session Here
		state := smContext.SMContextState
		if state == smf_context.InActivePending || state == smf_context.InActive {
			smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID = true
			logger.CtxLog.Infoln("[SMF] Cause_REL_DUE_TO_DUPLICATE_SESSION_ID")
			logger.CtxLog.Infof("Skip deleting the PFCP sessions of PDUSessionID:%d of SUPI:%s",
				smContext.PDUSessionID, smContext.Supi)
			return &httpwrapper.Response{
				Status: http.StatusOK,
				Body:   response,
			}
		} else if state != smf_context.Active {
			logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
				smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
			var httpResponse *httpwrapper.Response
			if buf, err := smf_context.
				BuildGSMPDUSessionEstablishmentReject(
					smContext,
					nasMessage.Cause5GSMRequestRejectedUnspecified); err != nil {
				httpResponse = &httpwrapper.Response{
					Header: nil,
					Status: http.StatusForbidden,
					Body: models.UpdateSmContextErrorResponse{
						JsonData: &models.SmContextUpdateError{
							Error: &Nsmf_PDUSession.SmContextStateMismatchActive,
						},
					},
				}
			} else {
				httpResponse = &httpwrapper.Response{
					Header: nil,
					Status: http.StatusForbidden,
					Body: models.UpdateSmContextErrorResponse{
						JsonData: &models.SmContextUpdateError{
							Error:   &Nsmf_PDUSession.SmContextStateMismatchActive,
							N1SmMsg: &models.RefToBinaryData{ContentId: "n1SmMsg"},
						},
						BinaryDataN1SmMessage: buf,
					},
				}
			}
			return httpResponse
		}

		smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID = true
		logger.CtxLog.Infoln("[SMF] Cause_REL_DUE_TO_DUPLICATE_SESSION_ID")
		pfcpResponseStatus = releaseSession(smContext)
	}

	var httpResponse *httpwrapper.Response
	// Check FSM and take corresponding action
	switch smContext.SMContextState {
	case smf_context.PFCPModification:
		logger.CtxLog.Traceln("In case PFCPModification")

		if sendPFCPModification {
			pfcpResponseStatus = updateAnUpfPfcpSession(smContext, pdrList, farList, barList, qerList)
		}

		switch pfcpResponseStatus {
		case smf_context.SessionUpdateSuccess:
			logger.CtxLog.Traceln("In case SessionUpdateSuccess")
			smContext.SMContextState = smf_context.Active
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			httpResponse = &httpwrapper.Response{
				Status: http.StatusOK,
				Body:   response,
			}
		case smf_context.SessionUpdateFailed:
			logger.CtxLog.Traceln("In case SessionUpdateFailed")
			smContext.SMContextState = smf_context.Active
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			// It is just a template
			httpResponse = &httpwrapper.Response{
				Status: http.StatusForbidden,
				Body: models.UpdateSmContextErrorResponse{
					JsonData: &models.SmContextUpdateError{
						Error: &Nsmf_PDUSession.N1SmError,
					},
				}, // Depends on the reason why N4 fail
			}

		case smf_context.SessionReleaseSuccess:
			logger.CtxLog.Traceln("In case SessionReleaseSuccess")
			smContext.SMContextState = smf_context.InActivePending
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			httpResponse = &httpwrapper.Response{
				Status: http.StatusOK,
				Body:   response,
			}

		case smf_context.SessionReleaseFailed:
			// Update SmContext Request(N1 PDU Session Release Request)
			// Send PDU Session Release Reject
			logger.CtxLog.Traceln("In case SessionReleaseFailed")
			problemDetail := models.ProblemDetails{
				Status: http.StatusInternalServerError,
				Cause:  "SYSTEM_FAILULE",
			}
			httpResponse = &httpwrapper.Response{
				Status: int(problemDetail.Status),
			}
			smContext.SMContextState = smf_context.Active
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			errResponse := models.UpdateSmContextErrorResponse{
				JsonData: &models.SmContextUpdateError{
					Error: &problemDetail,
				},
			}
			if smContextUpdateData.Cause == models.Cause_REL_DUE_TO_DUPLICATE_SESSION_ID {
				if buf, err := smf_context.BuildGSMPDUSessionEstablishmentReject(smContext,
					nasMessage.Cause5GSMNetworkFailure); err != nil {
					logger.PduSessLog.Errorf("build GSM PDUSessionEstablishmentReject failed: %+v", err)
				} else {
					errResponse.BinaryDataN1SmMessage = buf
					errResponse.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionEstablishmentReject"}
				}
			} else {
				if buf, err := smf_context.BuildGSMPDUSessionReleaseReject(smContext); err != nil {
					logger.PduSessLog.Errorf("build GSM PDUSessionReleaseReject failed: %+v", err)
				} else {
					errResponse.BinaryDataN1SmMessage = buf
					errResponse.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseReject"}
				}
			}
			httpResponse.Body = errResponse
		}
	case smf_context.ModificationPending:
		logger.CtxLog.Traceln("In case ModificationPending")
		smContext.SMContextState = smf_context.Active
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		httpResponse = &httpwrapper.Response{
			Status: http.StatusOK,
			Body:   response,
		}
	case smf_context.InActive, smf_context.InActivePending:
		logger.CtxLog.Traceln("In case InActive, InActivePending")
		httpResponse = &httpwrapper.Response{
			Status: http.StatusOK,
			Body:   response,
		}
	default:
		logger.PduSessLog.Warnf("SM Context State [%s] shouldn't be here\n", smContext.SMContextState)
		httpResponse = &httpwrapper.Response{
			Status: http.StatusOK,
			Body:   response,
		}
	}

	return httpResponse
}

func HandlePDUSessionSMContextRelease(smContextRef string, body models.ReleaseSmContextRequest) *httpwrapper.Response {
	logger.PduSessLog.Infoln("In HandlePDUSessionSMContextRelease")
	smContext := smf_context.GetSMContextByRef(smContextRef)

	if smContext == nil {
		logger.PduSessLog.Warnf("SMContext[%s] is not found", smContextRef)

		httpResponse := &httpwrapper.Response{
			Header: nil,
			Status: http.StatusNotFound,
			Body: models.UpdateSmContextErrorResponse{
				JsonData: &models.SmContextUpdateError{
					UpCnxState: models.UpCnxState_DEACTIVATED,
					Error: &models.ProblemDetails{
						Type:   "Resource Not Found",
						Title:  "SMContext Ref is not found",
						Status: http.StatusNotFound,
					},
				},
			},
		}
		return httpResponse
	}

	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	// remove SM Policy Association
	if smContext.SMPolicyID != "" {
		if err := consumer.SendSMPolicyAssociationTermination(smContext); err != nil {
			logger.PduSessLog.Errorf("SM Policy Termination failed: %s", err)
		} else {
			smContext.SMPolicyID = ""
		}
	}

	state := smContext.SMContextState
	if state == smf_context.InActivePending || state == smf_context.InActive {
		smf_context.RemoveSMContext(smContext.Ref)
		return &httpwrapper.Response{
			Status: http.StatusNoContent,
			Body:   nil,
		}
	} else if state != smf_context.Active {
		logger.PduSessLog.Warnf("SMContext[%s-%02d] should be Active, but actual %s",
			smContext.Supi, smContext.PDUSessionID, smContext.SMContextState.String())
		httpResponse := &httpwrapper.Response{
			Status: http.StatusForbidden,
			Body:   &Nsmf_PDUSession.SmContextStateMismatchActive,
		}
		return httpResponse
	}

	smContext.SMContextState = smf_context.PFCPModification
	logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())

	pfcpResponseStatus := releaseSession(smContext)

	var httpResponse *httpwrapper.Response

	switch pfcpResponseStatus {
	case smf_context.SessionReleaseSuccess:
		logger.CtxLog.Traceln("In case SessionReleaseSuccess")
		smContext.SMContextState = smf_context.InActivePending
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		httpResponse = &httpwrapper.Response{
			Status: http.StatusNoContent,
			Body:   nil,
		}

	case smf_context.SessionReleaseFailed:
		// Update SmContext Request(N1 PDU Session Release Request)
		// Send PDU Session Release Reject
		logger.CtxLog.Traceln("In case SessionReleaseFailed")
		problemDetail := models.ProblemDetails{
			Status: http.StatusInternalServerError,
			Cause:  "SYSTEM_FAILURE",
		}
		httpResponse = &httpwrapper.Response{
			Status: int(problemDetail.Status),
		}
		smContext.SMContextState = smf_context.Active
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		errResponse := models.UpdateSmContextErrorResponse{
			JsonData: &models.SmContextUpdateError{
				Error: &problemDetail,
			},
		}
		if buf, err := smf_context.BuildGSMPDUSessionReleaseReject(smContext); err != nil {
			logger.PduSessLog.Errorf("Build GSM PDUSessionReleaseReject failed: %+v", err)
		} else {
			errResponse.BinaryDataN1SmMessage = buf
			errResponse.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseReject"}
		}

		httpResponse.Body = errResponse
	default:
		logger.CtxLog.Warnf("The state shouldn't be [%s]", pfcpResponseStatus)

		logger.CtxLog.Traceln("In case Unknown")
		problemDetail := models.ProblemDetails{
			Status: http.StatusInternalServerError,
			Cause:  "SYSTEM_FAILURE",
		}
		httpResponse = &httpwrapper.Response{
			Status: int(problemDetail.Status),
		}
		smContext.SMContextState = smf_context.Active
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		errResponse := models.UpdateSmContextErrorResponse{
			JsonData: &models.SmContextUpdateError{
				Error: &problemDetail,
			},
		}
		if buf, err := smf_context.BuildGSMPDUSessionReleaseReject(smContext); err != nil {
			logger.PduSessLog.Errorf("Build GSM PDUSessionReleaseReject failed: %+v", err)
		} else {
			errResponse.BinaryDataN1SmMessage = buf
			errResponse.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseReject"}
		}

		httpResponse.Body = errResponse
	}

	smf_context.RemoveSMContext(smContext.Ref)

	return httpResponse
}

func HandlePDUSessionSMContextLocalRelease(smContext *smf_context.SMContext, createData *models.SmContextCreateData) {
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	// remove SM Policy Association
	if smContext.SMPolicyID != "" {
		if err := consumer.SendSMPolicyAssociationTermination(smContext); err != nil {
			logger.PduSessLog.Errorf("SM Policy Termination failed: %s", err)
		} else {
			smContext.SMPolicyID = ""
		}
	}

	if !(smContext.SMContextState == smf_context.InActivePending ||
		smContext.SMContextState == smf_context.InActive ||
		smContext.SMContextState == smf_context.ActivePending) {
		logger.CtxLog.Traceln("SMContext is still ACTIVE, Send PFCP session deletion request")
		smContext.SMContextState = smf_context.PFCPModification
		logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())

		pfcpResponseStatus := releaseSession(smContext)

		switch pfcpResponseStatus {
		case smf_context.SessionReleaseSuccess:
			logger.CtxLog.Traceln("In case SessionReleaseSuccess")
			smContext.SMContextState = smf_context.InActivePending
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
			if createData.SmContextStatusUri != smContext.SmStatusNotifyUri {
				problemDetails, err := consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)
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
			smf_context.RemoveSMContext(smContext.Ref)

		case smf_context.SessionReleaseFailed:
			logger.CtxLog.Traceln("In case SessionReleaseFailed")
			smContext.SMContextState = smf_context.Active
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())

		default:
			logger.CtxLog.Warnf("The state shouldn't be [%s]", pfcpResponseStatus)
			logger.CtxLog.Traceln("In case Unknown")
			smContext.SMContextState = smf_context.Active
			logger.CtxLog.Traceln("SMContextState Change State: ", smContext.SMContextState.String())
		}
	}
}

func releaseSession(smContext *smf_context.SMContext) smf_context.PFCPSessionResponseStatus {
	smContext.SMContextState = smf_context.PFCPModification

	for _, res := range ReleaseTunnel(smContext) {
		if res.Status != smf_context.SessionReleaseSuccess {
			return res.Status
		}
	}
	return smf_context.SessionReleaseSuccess
}

func makeErrorResponse(smContext *smf_context.SMContext, nasErrorCause uint8,
	sbiError *models.ProblemDetails,
) *httpwrapper.Response {
	var httpResponse *httpwrapper.Response

	if buf, err := smf_context.
		BuildGSMPDUSessionEstablishmentReject(
			smContext,
			nasErrorCause); err != nil {
		httpResponse = &httpwrapper.Response{
			Header: nil,
			Status: int(sbiError.Status),
			Body: models.PostSmContextsErrorResponse{
				JsonData: &models.SmContextCreateError{
					Error: sbiError,
				},
			},
		}
	} else {
		httpResponse = &httpwrapper.Response{
			Header: nil,
			Status: int(sbiError.Status),
			Body: models.PostSmContextsErrorResponse{
				JsonData: &models.SmContextCreateError{
					Error:   sbiError,
					N1SmMsg: &models.RefToBinaryData{ContentId: "n1SmMsg"},
				},
				BinaryDataN1SmMessage: buf,
			},
		}
	}
	return httpResponse
}
