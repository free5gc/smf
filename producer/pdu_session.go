package producer

import (
	"context"
	"encoding/json"
	"fmt"
	"free5gc/lib/http_wrapper"
	"free5gc/lib/nas"
	"free5gc/lib/nas/nasConvert"
	"free5gc/lib/openapi"
	"free5gc/lib/openapi/Namf_Communication"
	"free5gc/lib/openapi/Nsmf_PDUSession"
	"free5gc/lib/openapi/Nudm_SubscriberDataManagement"
	"free5gc/lib/openapi/models"
	"free5gc/lib/pfcp/pfcpType"
	"free5gc/src/smf/consumer"
	smf_context "free5gc/src/smf/context"
	smf_message "free5gc/src/smf/handler/message"
	"free5gc/src/smf/logger"
	pfcp_message "free5gc/src/smf/pfcp/message"
	"net/http"

	"github.com/antihax/optional"
)

func HandlePDUSessionSMContextCreate(rspChan chan smf_message.HandlerResponseMessage, request models.PostSmContextsRequest) {
	var err error
	var response models.PostSmContextsResponse
	response.JsonData = new(models.SmContextCreatedData)

	// Check has PDU Session Establishment Request
	m := nas.NewMessage()
	err = m.GsmMessageDecode(&request.BinaryDataN1SmMessage)
	if err != nil || m.GsmHeader.GetMessageType() != nas.MsgTypePDUSessionEstablishmentRequest {
		rspChan <- smf_message.HandlerResponseMessage{
			HTTPResponse: &http_wrapper.Response{
				Header: nil,
				Status: http.StatusForbidden,
				Body: models.PostSmContextsErrorResponse{
					JsonData: &models.SmContextCreateError{
						Error: &Nsmf_PDUSession.N1SmError,
					},
				},
			},
		}
		return
	}

	createData := request.JsonData
	smContext := smf_context.NewSMContext(createData.Supi, createData.PduSessionId)
	smContext.SetCreateData(createData)
	smContext.SmStatusNotifyUri = createData.SmContextStatusUri

	// Query UDM
	consumer.SendNFDiscoveryUDM()

	smPlmnID := createData.Guami.PlmnId

	smDataParams := &Nudm_SubscriberDataManagement.GetSmDataParamOpts{
		Dnn:         optional.NewString(createData.Dnn),
		PlmnId:      optional.NewInterface(smPlmnID.Mcc + smPlmnID.Mnc),
		SingleNssai: optional.NewInterface(openapi.MarshToJsonString(smContext.Snssai)),
	}

	SubscriberDataManagementClient := smf_context.SMF_Self().SubscriberDataManagementClient

	sessSubData, _, err := SubscriberDataManagementClient.SessionManagementSubscriptionDataRetrievalApi.GetSmData(context.Background(), smContext.Supi, smDataParams)

	if err != nil {
		logger.PduSessLog.Errorln("Get SessionManagementSubscriptionData error:", err)
	}

	if len(sessSubData) > 0 {
		smContext.DnnConfiguration = sessSubData[0].DnnConfigurations[smContext.Dnn]
	} else {
		logger.PduSessLog.Errorln("SessionManagementSubscriptionData from UDM is nil")
	}

	establishmentRequest := m.PDUSessionEstablishmentRequest
	smContext.HandlePDUSessionEstablishmentRequest(establishmentRequest)

	logger.PduSessLog.Infof("PCF Selection for SMContext SUPI[%s] PDUSessionID[%d]\n", smContext.Supi, smContext.PDUSessionID)
	err = smContext.PCFSelection()

	if err != nil {
		logger.PduSessLog.Errorln("pcf selection error:", err)
	}

	smPolicyData := models.SmPolicyContextData{}

	smPolicyData.Supi = smContext.Supi
	smPolicyData.PduSessionId = smContext.PDUSessionID
	smPolicyData.NotificationUri = fmt.Sprintf("%s://%s:%d/nsmf-callback/sm-policies/%s", smf_context.SMF_Self().URIScheme, smf_context.SMF_Self().HTTPAddress, smf_context.SMF_Self().HTTPPort, smContext.Ref)
	smPolicyData.Dnn = smContext.Dnn
	smPolicyData.PduSessionType = nasConvert.PDUSessionTypeToModels(smContext.SelectedPDUSessionType)
	smPolicyData.AccessType = smContext.AnType
	smPolicyData.RatType = smContext.RatType
	smPolicyData.Ipv4Address = smContext.PDUAddress.To4().String()
	smPolicyData.SubsSessAmbr = smContext.DnnConfiguration.SessionAmbr
	smPolicyData.SubsDefQos = smContext.DnnConfiguration.Var5gQosProfile
	smPolicyData.SliceInfo = smContext.Snssai
	smPolicyData.ServingNetwork = &models.NetworkId{
		Mcc: smContext.ServingNetwork.Mcc,
		Mnc: smContext.ServingNetwork.Mnc,
	}
	smPolicyData.SuppFeat = "F"

	smPolicyDecision, _, err := smContext.SMPolicyClient.DefaultApi.SmPoliciesPost(context.Background(), smPolicyData)

	if err != nil {
		openapiError := err.(openapi.GenericOpenAPIError)
		problemDetails := openapiError.Model().(models.ProblemDetails)
		logger.PduSessLog.Errorln("setup sm policy association failed:", err, problemDetails)
	}

	err = ApplySmPolicyFromDecision(smContext, &smPolicyDecision)

	if err != nil {
		logger.PduSessLog.Errorf("apply sm policy decision error: %v", err)
	}

	smContext.Tunnel = smf_context.NewUPTunnel()
	var defaultPath *smf_context.DataPath

	if smf_context.SMF_Self().ULCLSupport && smf_context.CheckUEHasPreConfig(createData.Supi) {
		//TODO: change UPFRoot => ULCL UserPlane Refactor
		logger.PduSessLog.Infof("SUPI[%s] has pre-config route", createData.Supi)
		uePreConfigPaths := smf_context.GetUEPreConfigPaths(createData.Supi)
		smContext.Tunnel.DataPathPool = uePreConfigPaths.DataPathPool
		smContext.Tunnel.PathIDGenerator = uePreConfigPaths.PathIDGenerator
		defaultPath = smContext.Tunnel.DataPathPool.GetDefaultPath()
		smContext.AllocateLocalSEIDForDataPath(defaultPath)
		defaultPath.ActivateTunnelAndPDR(smContext)
		// TODO: Maybe we don't need this
		smContext.BPManager = smf_context.NewBPManager(createData.Supi)
	} else {
		logger.PduSessLog.Infof("SUPI[%s] has no pre-config route", createData.Supi)
		defaultUPPath := smf_context.GetUserPlaneInformation().GetDefaultUserPlanePathByDNN(createData.Dnn)
		smContext.AllocateLocalSEIDForUPPath(defaultUPPath)
		defaultPath = smf_context.GenerateDataPath(defaultUPPath, smContext)
		defaultPath.IsDefaultPath = true
		smContext.Tunnel.AddDataPath(defaultPath)
		defaultPath.ActivateTunnelAndPDR(smContext)
	}

	if defaultPath == nil {
		logger.PduSessLog.Errorf("Path for serve DNN[%s] not found\n", createData.Dnn)
		rspChan <- smf_message.HandlerResponseMessage{
			HTTPResponse: &http_wrapper.Response{
				Header: nil,
				Status: http.StatusForbidden,
				Body: models.PostSmContextsErrorResponse{
					JsonData: &models.SmContextCreateError{
						Error:   &Nsmf_PDUSession.DnnNotSupported,
						N1SmMsg: &models.RefToBinaryData{ContentId: "N1Msg"},
					},
				},
			},
		}

	}

	response.JsonData = smContext.BuildCreatedData()
	rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &http_wrapper.Response{
		Header: http.Header{
			"Location": {smContext.Ref},
		},
		Status: http.StatusCreated,
		Body:   response,
	}}

	// TODO: UECM registration

	SendPFCPRule(smContext, defaultPath)
	logger.PduSessLog.Infof("SendPFCPRule Finshed")

	consumer.SendNFDiscoveryServingAMF(smContext)

	for _, service := range *smContext.AMFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NAMF_COMM {
			communicationConf := Namf_Communication.NewConfiguration()
			communicationConf.SetBasePath(service.ApiPrefix)
			smContext.CommunicationClient = Namf_Communication.NewAPIClient(communicationConf)
		}
	}

	logger.PduSessLog.Infof("HandlePDUSessionSMContextUpdate Finshed")

}

func HandlePDUSessionSMContextUpdate(rspChan chan smf_message.HandlerResponseMessage, smContextRef string, body models.UpdateSmContextRequest) (seqNum uint32, resBody models.UpdateSmContextResponse) {
	smContext := smf_context.GetSMContext(smContextRef)

	logger.PduSessLog.Infoln("[SMF] PDUSession SMContext Update")
	if smContext == nil {
		rspChan <- smf_message.HandlerResponseMessage{
			HTTPResponse: &http_wrapper.Response{
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
			},
		}
		return
	}

	var response models.UpdateSmContextResponse
	response.JsonData = new(models.SmContextUpdatedData)

	smContextUpdateData := body.JsonData

	UpdateSmContextRequestJson, _ := json.Marshal(body.JsonData)
	logger.PduSessLog.Traceln("[SMF] UpdateSmContextRequest JsonData: ", string(UpdateSmContextRequestJson))

	if body.BinaryDataN1SmMessage != nil {
		logger.PduSessLog.Traceln("Binary Data N1 SmMessage isn't nil!")
		m := nas.NewMessage()
		err := m.GsmMessageDecode(&body.BinaryDataN1SmMessage)
		logger.PduSessLog.Traceln("[SMF] UpdateSmContextRequest N1SmMessage: ", m)
		if err != nil {
			logger.PduSessLog.Error(err)
			return
		}
		switch m.GsmHeader.GetMessageType() {
		case nas.MsgTypePDUSessionReleaseRequest:
			smContext.HandlePDUSessionReleaseRequest(m.PDUSessionReleaseRequest)
			buf, _ := smf_context.BuildGSMPDUSessionReleaseCommand(smContext)
			response.BinaryDataN1SmMessage = buf
			response.JsonData.N1SmMsg = &models.RefToBinaryData{ContentId: "PDUSessionReleaseCommand"}

			response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUResourceReleaseCommand"}
			response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_REL_CMD

			buf, err := smf_context.BuildPDUSessionResourceReleaseCommandTransfer(smContext)
			response.BinaryDataN2SmInformation = buf
			if err != nil {
				logger.PduSessLog.Error(err)
			}

			deletedPFCPNode := make(map[string]bool)

			for _, dataPath := range smContext.Tunnel.DataPathPool {

				dataPath.DeactivateTunnelAndPDR(smContext)
				for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode.Next() {
					curUPFID, _ := curDataPathNode.GetUPFID()
					if _, exist := deletedPFCPNode[curUPFID]; !exist {
						seqNum = pfcp_message.SendPfcpSessionDeletionRequest(curDataPathNode.UPF.NodeID, smContext)
						deletedPFCPNode[curUPFID] = true
					}
				}
			}

			return seqNum, response
		case nas.MsgTypePDUSessionReleaseComplete:
			// Send Release Notify to AMF
			logger.PduSessLog.Infoln("[SMF] Send Update SmContext Response")
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
			SMContextUpdateResponse := http_wrapper.Response{
				Status: http.StatusOK,
				Body:   response,
			}
			rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &SMContextUpdateResponse}

			smf_context.RemoveSMContext(smContext.Ref)
			consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)

			return
		}

	} else {
		logger.PduSessLog.Traceln("[SMF] Binary Data N1 SmMessage is nil!")
	}

	tunnel := smContext.Tunnel
	pdrList := []*smf_context.PDR{}
	farList := []*smf_context.FAR{}
	barList := []*smf_context.BAR{}

	switch smContextUpdateData.UpCnxState {
	case models.UpCnxState_ACTIVATING:
		response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUSessionResourceSetupRequestTransfer"}
		response.JsonData.UpCnxState = models.UpCnxState_ACTIVATING
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ

		n2Buf, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext)
		if err != nil {
			logger.PduSessLog.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		}
		response.BinaryDataN2SmInformation = n2Buf
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ
	case models.UpCnxState_DEACTIVATED:
		response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
		smContext.UpCnxState = body.JsonData.UpCnxState
		smContext.UeLocation = body.JsonData.UeLocation
		// TODO: Deactivate N2 downlink tunnel
		//Set FAR and An, N3 Release Info
		farList = []*smf_context.FAR{}

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
			}

			farList = append(farList, DLPDR.FAR)
		}

	}

	var err error

	switch smContextUpdateData.N2SmInfoType {
	case models.N2SmInfoType_PDU_RES_SETUP_RSP:
		pdrList = []*smf_context.PDR{}
		farList = []*smf_context.FAR{}

		for _, dataPath := range tunnel.DataPathPool {

			if dataPath.Activated {
				ANUPF := dataPath.FirstDPNode
				DLPDR := ANUPF.DownLinkTunnel.PDR

				DLPDR.FAR.ApplyAction = pfcpType.ApplyAction{Buff: false, Drop: false, Dupl: false, Forw: true, Nocp: false}
				DLPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
					DestinationInterface: pfcpType.DestinationInterface{
						InterfaceValue: pfcpType.DestinationInterfaceAccess,
					},
					NetworkInstance: []byte(smContext.Dnn),
				}

				DLPDR.State = smf_context.RULE_UPDATE
				DLPDR.FAR.State = smf_context.RULE_UPDATE

				pdrList = append(pdrList, DLPDR)
				farList = append(farList, DLPDR.FAR)
			}

		}

		err = smf_context.HandlePDUSessionResourceSetupResponseTransfer(body.BinaryDataN2SmInformation, smContext)

	case models.N2SmInfoType_PDU_RES_REL_RSP:
		logger.PduSessLog.Infoln("[SMF] N2 PDUSession Release Complete ")
		if smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID {
			logger.PduSessLog.Infoln("[SMF] Send Update SmContext Response")
			response.JsonData.UpCnxState = models.UpCnxState_DEACTIVATED
			SMContextUpdateResponse := http_wrapper.Response{
				Status: http.StatusOK,
				Body:   response,
			}
			rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &SMContextUpdateResponse}

			smf_context.RemoveSMContext(smContext.Ref)
			consumer.SendSMContextStatusNotification(smContext.SmStatusNotifyUri)

			smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID = false
			return

		} else { // normal case
			logger.PduSessLog.Infoln("[SMF] Send Update SmContext Response")
			SMContextUpdateResponse := http_wrapper.Response{
				Status: http.StatusOK,
				Body:   response,
			}
			rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &SMContextUpdateResponse}

			return
		}
	case models.N2SmInfoType_PATH_SWITCH_REQ:
		logger.PduSessLog.Traceln("Handle Path Switch Request")
		err = smf_context.HandlePathSwitchRequestTransfer(body.BinaryDataN2SmInformation, smContext)
		n2Buf, err := smf_context.BuildPathSwitchRequestAcknowledgeTransfer(smContext)
		if err != nil {
			logger.PduSessLog.Errorf("Build Path Switch Transfer Error(%s)", err.Error())
		}

		response.BinaryDataN2SmInformation = n2Buf
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PATH_SWITCH_REQ_ACK
		response.JsonData.N2SmInfo = &models.RefToBinaryData{
			ContentId: "PATH_SWITCH_REQ_ACK",
		}

		for _, dataPath := range tunnel.DataPathPool {

			if dataPath.Activated {
				ANUPF := dataPath.FirstDPNode
				DLPDR := ANUPF.DownLinkTunnel.PDR

				pdrList = append(pdrList, DLPDR)
				farList = append(farList, DLPDR.FAR)
			}
		}

	case models.N2SmInfoType_PATH_SWITCH_SETUP_FAIL:
		err = smf_context.HandlePathSwitchRequestSetupFailedTransfer(body.BinaryDataN2SmInformation, smContext)
	case models.N2SmInfoType_HANDOVER_REQUIRED:
		response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "Handover"}
	}

	switch smContextUpdateData.HoState {
	case models.HoState_PREPARING:
		smContext.HoState = models.HoState_PREPARING
		err = smf_context.HandleHandoverRequiredTransfer(body.BinaryDataN2SmInformation, smContext)
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ

		n2Buf, err := smf_context.BuildPDUSessionResourceSetupRequestTransfer(smContext)
		if err != nil {
			logger.PduSessLog.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		}
		response.BinaryDataN2SmInformation = n2Buf
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_SETUP_REQ
		response.JsonData.N2SmInfo = &models.RefToBinaryData{
			ContentId: "PDU_RES_SETUP_REQ",
		}
		response.JsonData.HoState = models.HoState_PREPARING
	case models.HoState_PREPARED:
		smContext.HoState = models.HoState_PREPARED
		response.JsonData.HoState = models.HoState_PREPARED
		err = smf_context.HandleHandoverRequestAcknowledgeTransfer(body.BinaryDataN2SmInformation, smContext)
		n2Buf, err := smf_context.BuildHandoverCommandTransfer(smContext)
		if err != nil {
			logger.PduSessLog.Errorf("Build PDUSession Resource Setup Request Transfer Error(%s)", err.Error())
		}
		response.BinaryDataN2SmInformation = n2Buf
		response.JsonData.N2SmInfoType = models.N2SmInfoType_HANDOVER_CMD
		response.JsonData.N2SmInfo = &models.RefToBinaryData{
			ContentId: "HANDOVER_CMD",
		}
		response.JsonData.HoState = models.HoState_PREPARING
	case models.HoState_COMPLETED:
		smContext.HoState = models.HoState_COMPLETED
		response.JsonData.HoState = models.HoState_COMPLETED
	}

	switch smContextUpdateData.Cause {

	case models.Cause_REL_DUE_TO_DUPLICATE_SESSION_ID:
		//* release PDU Session Here

		response.JsonData.N2SmInfo = &models.RefToBinaryData{ContentId: "PDUResourceReleaseCommand"}
		response.JsonData.N2SmInfoType = models.N2SmInfoType_PDU_RES_REL_CMD
		smContext.PDUSessionRelease_DUE_TO_DUP_PDU_ID = true

		buf, err := smf_context.BuildPDUSessionResourceReleaseCommandTransfer(smContext)
		response.BinaryDataN2SmInformation = buf
		if err != nil {
			logger.PduSessLog.Error(err)
		}

		deletedPFCPNode := make(map[string]bool)
		for _, dataPath := range smContext.Tunnel.DataPathPool {

			dataPath.DeactivateTunnelAndPDR(smContext)
			for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode.Next() {
				curUPFID, _ := curDataPathNode.GetUPFID()
				if _, exist := deletedPFCPNode[curUPFID]; !exist {
					seqNum = pfcp_message.SendPfcpSessionDeletionRequest(curDataPathNode.UPF.NodeID, smContext)
					deletedPFCPNode[curUPFID] = true
				}
			}
		}

		fmt.Println("[SMF] Cause_REL_DUE_TO_DUPLICATE_SESSION_ID")
		return seqNum, response
	}

	if err != nil {
		logger.PduSessLog.Error(err)
	}

	defaultPath := smContext.Tunnel.DataPathPool.GetDefaultPath()
	ANUPF := defaultPath.FirstDPNode

	seqNum = pfcp_message.SendPfcpSessionModificationRequest(ANUPF.UPF.NodeID, smContext, pdrList, farList, barList)

	return seqNum, response
}

func HandlePDUSessionSMContextRelease(rspChan chan smf_message.HandlerResponseMessage, smContextRef string, body models.ReleaseSmContextRequest) (seqNum uint32) {
	smContext := smf_context.GetSMContext(smContextRef)
	// smf_context.RemoveSMContext(smContext.Ref)
	deletedPFCPNode := make(map[string]bool)
	for _, dataPath := range smContext.Tunnel.DataPathPool {

		dataPath.DeactivateTunnelAndPDR(smContext)
		for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode.Next() {
			curUPFID, _ := curDataPathNode.GetUPFID()
			if _, exist := deletedPFCPNode[curUPFID]; !exist {
				seqNum = pfcp_message.SendPfcpSessionDeletionRequest(curDataPathNode.UPF.NodeID, smContext)
				deletedPFCPNode[curUPFID] = true
			}
		}
	}

	return seqNum

	// rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &http_wrapper.Response{
	// 	Header: nil,
	// 	Status: http.StatusNoContent,
	// 	Body:   nil,
	// }}
}

func SendPFCPRule(smContext *smf_context.SMContext, dataPath *smf_context.DataPath) {

	logger.PduSessLog.Infof("Send PFCP Rule")
	logger.PduSessLog.Infof("DataPath: ", dataPath)
	for curDataPathNode := dataPath.FirstDPNode; curDataPathNode != nil; curDataPathNode = curDataPathNode.Next() {
		pdrList := make([]*smf_context.PDR, 0, 2)
		farList := make([]*smf_context.FAR, 0, 2)
		if !curDataPathNode.HaveSession {
			if curDataPathNode.UpLinkTunnel != nil && curDataPathNode.UpLinkTunnel.PDR != nil {
				pdrList = append(pdrList, curDataPathNode.UpLinkTunnel.PDR)
				farList = append(farList, curDataPathNode.UpLinkTunnel.PDR.FAR)
			}
			if curDataPathNode.DownLinkTunnel != nil && curDataPathNode.DownLinkTunnel.PDR != nil {
				pdrList = append(pdrList, curDataPathNode.DownLinkTunnel.PDR)
				farList = append(farList, curDataPathNode.DownLinkTunnel.PDR.FAR)
			}

			pfcp_message.SendPfcpSessionEstablishmentRequest(curDataPathNode.UPF.NodeID, smContext, pdrList, farList, nil)
			curDataPathNode.HaveSession = true
		} else {
			if curDataPathNode.UpLinkTunnel != nil && curDataPathNode.UpLinkTunnel.PDR != nil {
				pdrList = append(pdrList, curDataPathNode.UpLinkTunnel.PDR)
				farList = append(farList, curDataPathNode.UpLinkTunnel.PDR.FAR)
			}
			if curDataPathNode.DownLinkTunnel != nil && curDataPathNode.DownLinkTunnel.PDR != nil {
				pdrList = append(pdrList, curDataPathNode.DownLinkTunnel.PDR)
				farList = append(farList, curDataPathNode.DownLinkTunnel.PDR.FAR)
			}

			pfcp_message.SendPfcpSessionModificationRequest(curDataPathNode.UPF.NodeID, smContext, pdrList, farList, nil)
		}

	}
}
