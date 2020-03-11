package smf_producer

import (
	"context"
	"fmt"
	"gofree5gc/lib/Namf_Communication"
	"gofree5gc/lib/Nsmf_PDUSession"
	"gofree5gc/lib/Nudm_SubscriberDataManagement"
	"gofree5gc/lib/http_wrapper"
	"gofree5gc/lib/nas"
	"gofree5gc/lib/nas/nasConvert"
	"gofree5gc/lib/openapi"
	"gofree5gc/lib/openapi/common"
	"gofree5gc/lib/openapi/models"
	"gofree5gc/lib/pfcp/pfcpType"
	"gofree5gc/lib/pfcp/pfcpUdp"
	"gofree5gc/src/smf/logger"
	"gofree5gc/src/smf/smf_consumer"
	"gofree5gc/src/smf/smf_context"
	"gofree5gc/src/smf/smf_handler/smf_message"
	"gofree5gc/src/smf/smf_pfcp/pfcp_message"
	"net"
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
	smf_consumer.SendNFDiscoveryUDM()

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

	if sessSubData != nil && len(sessSubData) > 0 {
		smContext.DnnConfiguration = sessSubData[0].DnnConfigurations[smContext.Dnn]
	} else {
		logger.PduSessLog.Errorln("SessionManagementSubscriptionData from UDM is nil")
	}

	establishmentRequest := m.PDUSessionEstablishmentRequest
	smContext.HandlePDUSessionEstablishmentRequest(establishmentRequest)

	smPolicyData := models.SmPolicyContextData{}

	smPolicyData.Supi = smContext.Supi
	smPolicyData.PduSessionId = smContext.PDUSessionID
	smPolicyData.NotificationUri = fmt.Sprintf("https://%s:%d/", smf_context.SMF_Self().HTTPAddress, smf_context.SMF_Self().HTTPPort)
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

	err = smContext.PCFSelection()

	if err != nil {
		logger.PduSessLog.Errorln("pcf selection error:", err)
	}

	smPolicyDicirion, _, err := smContext.SMPolicyClient.DefaultApi.SmPoliciesPost(context.Background(), smPolicyData)

	if err != nil {
		openapiError := err.(common.GenericOpenAPIError)
		problemDetails := openapiError.Model().(models.ProblemDetails)
		logger.PduSessLog.Errorln("setup sm policy association failed:", err, problemDetails)
	}

	for _, sessRule := range smPolicyDicirion.SessRules {
		smContext.SessionRule = sessRule
		break
	}

	var upfRoot *smf_context.DataPathNode
	if smf_context.CheckUEHasPreConfig(createData.Supi) {

		ueRoutingGraph := smf_context.GetUERoutingGraph(createData.Supi)
		upfRoot = ueRoutingGraph.GetGraphRoot()
		psaPath := smf_context.GetUserPlaneInformation().DefaultUserPlanePath[createData.Dnn]

		err := upfRoot.EnableUserPlanePath(psaPath)
		if err != nil {
			logger.PduSessLog.Error(err)
			return
		}

		smContext.BPManager = smf_context.NewBPManager(createData.Supi)
		smContext.BPManager.SetPSAStatus(psaPath)
		smContext.BPManager.PSA1Path = psaPath
	} else {
		upfRoot = smf_context.GetUserPlaneInformation().GetDefaultUPFTopoByDNN(createData.Dnn)
	}

	if upfRoot == nil {

		logger.PduSessLog.Errorf("UPF for serve DNN[%s] not found\n", createData.Dnn)
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

	selectedUPF := smf_context.SelectUPFByDnn(createData.Dnn)
	if selectedUPF == nil {
		logger.PduSessLog.Errorf("UPF for serve DNN[%s] not found\n", createData.Dnn)
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
	smContext.Tunnel = new(smf_context.UPTunnel)

	SetUpUplinkUserPlane(upfRoot, smContext)
	smContext.Tunnel.UpfRoot = upfRoot

	smContext.Tunnel.Node = selectedUPF
	tunnel := smContext.Tunnel
	// Establish UP
	tunnel.ULTEID, err = tunnel.Node.GenerateTEID()

	if err != nil {
		logger.PduSessLog.Error(err)
	}

	tunnel.ULPDR, err = smContext.Tunnel.Node.AddPDR()

	if err != nil {
		logger.PduSessLog.Error(err)
	}

	tunnel.ULPDR.Precedence = 32
	tunnel.ULPDR.PDI = smf_context.PDI{
		SourceInterface: pfcpType.SourceInterface{
			InterfaceValue: pfcpType.SourceInterfaceAccess,
		},
		LocalFTeid: &pfcpType.FTEID{
			V4:          true,
			Teid:        tunnel.ULTEID,
			Ipv4Address: tunnel.Node.UPIPInfo.Ipv4Address,
		},
		NetworkInstance: []byte(smContext.Dnn),
		UEIPAddress: &pfcpType.UEIPAddress{
			V4:          true,
			Ipv4Address: smContext.PDUAddress.To4(),
		},
	}
	tunnel.ULPDR.OuterHeaderRemoval = new(pfcpType.OuterHeaderRemoval)
	tunnel.ULPDR.OuterHeaderRemoval.OuterHeaderRemovalDescription = pfcpType.OuterHeaderRemovalGtpUUdpIpv4

	tunnel.ULPDR.FAR.ApplyAction.Forw = true
	tunnel.ULPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
		DestinationInterface: pfcpType.DestinationInterface{
			InterfaceValue: pfcpType.DestinationInterfaceCore,
		},
		NetworkInstance: []byte(smContext.Dnn),
	}

	logger.PduSessLog.Infof("PCF Selection for SMContext SUPI[%s] PDUSessionID[%d]\n", smContext.Supi, smContext.PDUSessionID)
	err = smContext.PCFSelection()
	if err != nil {
		logger.PduSessLog.Warnf("PCF Select failed: %s", err)
	}

	addr := net.UDPAddr{
		IP:   smContext.Tunnel.Node.NodeID.NodeIdValue,
		Port: pfcpUdp.PFCP_PORT,
	}

	fmt.Println("[SMF] Send PFCP to UPF IP: ", addr.IP.String())
	// TODO: remove it when UL/CL apply
	//pfcp_message.SendPfcpSessionEstablishmentRequest(&addr, smContext)
	//AddUEUpLinkRoutingInfo(smContext)

	smf_consumer.SendNFDiscoveryServingAMF(smContext)

	// Workaround AMF Profile
	// smContext.AMFProfile = models.NfProfile{
	// 	NfServices: &[]models.NfService{
	// 		{
	// 			ServiceName: models.ServiceName_NAMF_COMM,
	// 			ApiPrefix:   "https://127.0.0.1:29518",
	// 		},
	// 	},
	// }

	for _, service := range *smContext.AMFProfile.NfServices {
		if service.ServiceName == models.ServiceName_NAMF_COMM {
			communicationConf := Namf_Communication.NewConfiguration()
			communicationConf.SetBasePath(service.ApiPrefix)
			smContext.CommunicationClient = Namf_Communication.NewAPIClient(communicationConf)
		}
	}
}

func HandlePDUSessionSMContextUpdate(rspChan chan smf_message.HandlerResponseMessage, smContextRef string, body models.UpdateSmContextRequest) (seqNum uint32, resBody models.UpdateSmContextResponse) {
	smContext := smf_context.GetSMContext(smContextRef)

	if smContext == nil {
		rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &http_wrapper.Response{
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
		}}
		return
	}

	var response models.UpdateSmContextResponse
	response.JsonData = new(models.SmContextUpdatedData)

	smContextUpdateData := body.JsonData

	if body.BinaryDataN1SmMessage != nil {
		m := nas.NewMessage()
		err := m.GsmMessageDecode(&body.BinaryDataN1SmMessage)
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
			addr := net.UDPAddr{
				IP:   smContext.Tunnel.Node.NodeID.NodeIdValue,
				Port: pfcpUdp.PFCP_PORT,
			}

			seqNum = pfcp_message.SendPfcpSessionDeletionRequest(&addr, smContext)
			return seqNum, response
		}

	}

	tunnel := smContext.Tunnel
	pdr_list := []*smf_context.PDR{}
	far_list := []*smf_context.FAR{}
	bar_list := []*smf_context.BAR{}

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
		if tunnel.DLPDR == nil {
			logger.PduSessLog.Errorf("Release Error")
		} else {
			tunnel.DLPDR.FAR.State = smf_context.RULE_UPDATE
			tunnel.DLPDR.FAR.ApplyAction.Forw = false
			tunnel.DLPDR.FAR.ApplyAction.Buff = true
			tunnel.DLPDR.FAR.ApplyAction.Nocp = true

			if tunnel.DLPDR.FAR.BAR == nil {
				tunnel.DLPDR.FAR.BAR, _ = smContext.Tunnel.Node.AddBAR()
				bar_list = []*smf_context.BAR{tunnel.DLPDR.FAR.BAR}
			}

		}

		far_list = []*smf_context.FAR{tunnel.DLPDR.FAR}
	}

	var err error

	switch smContextUpdateData.N2SmInfoType {
	case models.N2SmInfoType_PDU_RES_SETUP_RSP:
		if tunnel.DLPDR == nil {
			tunnel.DLPDR, _ = smContext.Tunnel.Node.AddPDR()
		} else {
			tunnel.DLPDR.State = smf_context.RULE_UPDATE
		}

		//NodeID := tunnel.Node.NodeID
		// if CheckUEUpLinkRoutingStatus(smContext) == Uninitialized {
		// 	fmt.Println("[SMF] ======= In HandlePDUSessionSMContextUpdate ======")
		// 	fmt.Println("[SMF] Initialized UE UPLink Routing !")
		// 	fmt.Println("[SMF] In HandlePDUSessionSMContextUpdate Supi: ", smContext.Supi)
		// 	fmt.Println("[SMF] In HandlePDUSessionSMContextUpdate NodeID: ", NodeID.ResolveNodeIdToIp().String())

		// 	// TODO: Setup Uplink Routing
		// 	// InitializeUEUplinkRouting(smContext)
		// 	SetUeRoutingInitializeState(smContext, HasSendPFCPMsg)
		// }

		tunnel.DLPDR.Precedence = 32
		tunnel.DLPDR.PDI = smf_context.PDI{
			SourceInterface: pfcpType.SourceInterface{
				InterfaceValue: pfcpType.SourceInterfaceSgiLanN6Lan,
			},
			// LocalFTeid: &pfcpType.FTEID{
			// 	V4:          true,
			// 	Teid:        tunnel.ULTEID,
			// 	Ipv4Address: tunnel.Node.UPIPInfo.Ipv4Address,
			// },
			NetworkInstance: []byte(smContext.Dnn),
			UEIPAddress: &pfcpType.UEIPAddress{
				V4:          true,
				Ipv4Address: smContext.PDUAddress.To4(),
			},
		}

		tunnel.DLPDR.FAR.ApplyAction.Forw = true
		tunnel.DLPDR.FAR.ApplyAction.Nocp = false
		tunnel.DLPDR.FAR.ApplyAction.Drop = false
		tunnel.DLPDR.FAR.ApplyAction.Buff = false
		tunnel.DLPDR.FAR.ApplyAction.Dupl = false
		tunnel.DLPDR.FAR.ForwardingParameters = &smf_context.ForwardingParameters{
			DestinationInterface: pfcpType.DestinationInterface{
				InterfaceValue: pfcpType.DestinationInterfaceAccess,
			},
			NetworkInstance: []byte(smContext.Dnn),
		}
		err = smf_context.HandlePDUSessionResourceSetupResponseTransfer(body.BinaryDataN2SmInformation, smContext)

		pdr_list = []*smf_context.PDR{tunnel.DLPDR}
		far_list = []*smf_context.FAR{tunnel.DLPDR.FAR}

	case models.N2SmInfoType_PATH_SWITCH_REQ:
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

		pdr_list = []*smf_context.PDR{tunnel.DLPDR}
		far_list = []*smf_context.FAR{tunnel.DLPDR.FAR}

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

	if err != nil {
		logger.PduSessLog.Error(err)
	}

	addr := net.UDPAddr{
		IP:   smContext.Tunnel.Node.NodeID.NodeIdValue,
		Port: pfcpUdp.PFCP_PORT,
	}

	seqNum = pfcp_message.SendPfcpSessionModificationRequest(&addr, smContext, pdr_list, far_list, bar_list)

	return seqNum, response
}

func HandlePDUSessionSMContextRelease(rspChan chan smf_message.HandlerResponseMessage, smContextRef string, body models.ReleaseSmContextRequest) (seqNum uint32) {
	smContext := smf_context.GetSMContext(smContextRef)

	// smf_context.RemoveSMContext(smContext.Ref)

	addr := net.UDPAddr{
		IP:   smContext.Tunnel.Node.NodeID.NodeIdValue,
		Port: pfcpUdp.PFCP_PORT,
	}

	seqNum = pfcp_message.SendPfcpSessionDeletionRequest(&addr, smContext)
	return seqNum

	// rspChan <- smf_message.HandlerResponseMessage{HTTPResponse: &http_wrapper.Response{
	// 	Header: nil,
	// 	Status: http.StatusNoContent,
	// 	Body:   nil,
	// }}
}

// func SetUpAllUPF(node *smf_context.DataPathNode) {

// 	node.UPF.UPFStatus = smf_context.AssociatedSetUpSuccess

// 	for _, child_link := range node.DataPathToDN {

// 		SetUpAllUPF(child_link.To)
// 	}
// }
