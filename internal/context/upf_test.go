package context_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	nasie "github.com/free5gc/nas/ie"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/pfcp/pfcptype"
	"github.com/free5gc/smf/pkg/factory"
)

var mockIPv4NodeID = &pfcptype.NodeID{
	NodeIdType: pfcptype.NodeIdTypeIpv4Address,
	IP:         net.ParseIP("127.0.0.1"),
}

var mockIfaces = []*factory.InterfaceUpfInfoItem{
	{
		InterfaceType:    "N3",
		Endpoints:        []string{"127.0.0.1"},
		NetworkInstances: []string{"internet"},
	},
}

func convertPDUSessTypeToString(pduType uint8) string {
	switch pduType {
	case nasie.PDUSessType_IPv4:
		return "PDU Session Type IPv4"
	case nasie.PDUSessType_IPv6:
		return "PDU Session Type IPv6"
	case nasie.PDUSessType_IPv4v6:
		return "PDU Session Type IPv4 IPv6"
	case nasie.PDUSessType_Unstructured:
		return "PDU Session Type Unstructured"
	case nasie.PDUSessType_Ethernet:
		return "PDU Session Type Ethernet"
	}

	return "Unkwown PDU Session Type"
}

func TestIP(t *testing.T) {
	testCases := []struct {
		input               *smf_context.UPFInterfaceInfo
		inputPDUSessionType uint8
		paramStr            string
		resultStr           string
		expectedIP          string
		expectedError       error
	}{
		{
			input: &smf_context.UPFInterfaceInfo{
				NetworkInstances:      []string{""},
				IPv4EndPointAddresses: []net.IP{net.ParseIP("8.8.8.8")},
				IPv6EndPointAddresses: []net.IP{net.ParseIP("2001:4860:4860::8888")},
				EndpointFQDN:          "www.google.com",
			},
			inputPDUSessionType: nasie.PDUSessType_IPv4,
			paramStr:            "select " + convertPDUSessTypeToString(nasie.PDUSessType_IPv4),
			expectedIP:          "8.8.8.8",
			expectedError:       nil,
		},
		{
			input: &smf_context.UPFInterfaceInfo{
				NetworkInstances:      []string{""},
				IPv4EndPointAddresses: []net.IP{net.ParseIP("8.8.8.8")},
				IPv6EndPointAddresses: []net.IP{net.ParseIP("2001:4860:4860::8888")},
				EndpointFQDN:          "www.google.com",
			},
			inputPDUSessionType: nasie.PDUSessType_IPv6,
			paramStr:            "select " + convertPDUSessTypeToString(nasie.PDUSessType_IPv6),
			expectedIP:          "2001:4860:4860::8888",
			expectedError:       nil,
		},
	}

	Convey("Given UPFInterfaceInfo and select PDU Session type, should return correct IP", t, func() {
		for i, testcase := range testCases {
			upfInterfaceInfo := testcase.input
			infoStr := fmt.Sprintf("testcase[%d] UPF Interface Info: %+v", i, upfInterfaceInfo)

			Convey(infoStr, func() {
				Convey(testcase.paramStr, func() {
					ip, err := upfInterfaceInfo.IP(testcase.inputPDUSessionType)
					testcase.resultStr = "IP addr should be " + testcase.expectedIP

					Convey(testcase.resultStr, func() {
						So(ip.String(), ShouldEqual, testcase.expectedIP)
						So(err, ShouldEqual, testcase.expectedError)
					})
				})
			})
		}
	})
}

func TestAddDataPath(t *testing.T) {
	// AddDataPath is simple, should only have one case
	testCases := []struct {
		tunnel        *smf_context.UPTunnel
		addedDataPath *smf_context.DataPath
		resultStr     string
		expectedExist bool
	}{
		{
			tunnel:        smf_context.NewUPTunnel(),
			addedDataPath: smf_context.NewDataPath(),
			resultStr:     "Datapath should exist",
			expectedExist: true,
		},
	}

	Convey("AddDataPath should indeed add datapath", t, func() {
		for i, testcase := range testCases {
			upTunnel := testcase.tunnel
			infoStr := fmt.Sprintf("testcase[%d]: Add Datapath", i)

			Convey(infoStr, func() {
				upTunnel.AddDataPath(testcase.addedDataPath)

				Convey(testcase.resultStr, func() {
					var exist bool
					for _, datapath := range upTunnel.DataPathPool {
						if datapath == testcase.addedDataPath {
							exist = true
						}
					}
					So(exist, ShouldEqual, testcase.expectedExist)
				})
			})
		}
	})
}

func TestAddPDR(t *testing.T) {
	testCases := []struct {
		upf           *smf_context.UPF
		resultStr     string
		expectedError error
	}{
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddPDR should success",
			expectedError: nil,
		},
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddPDR should fail",
			expectedError: fmt.Errorf("UPF[127.0.0.1] not associated with SMF"),
		},
	}

	testCases[0].upf.EstablishAssociation(context.Background())

	Convey("AddPDR should indeed add PDR and report error appropiately", t, func() {
		for i, testcase := range testCases {
			upf := testcase.upf
			infoStr := fmt.Sprintf("testcase[%d]: ", i)

			Convey(infoStr, func() {
				_, err := upf.AddPDR()

				Convey(testcase.resultStr, func() {
					if testcase.expectedError == nil {
						So(err, ShouldBeNil)
					} else {
						So(err, ShouldNotBeNil)
						if err != nil {
							So(err.Error(), ShouldEqual, testcase.expectedError.Error())
						}
					}
				})
			})
		}
	})
}

func TestAddFAR(t *testing.T) {
	testCases := []struct {
		upf           *smf_context.UPF
		resultStr     string
		expectedError error
	}{
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddFAR should success",
			expectedError: nil,
		},
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddFAR should fail",
			expectedError: fmt.Errorf("UPF[127.0.0.1] not associated with SMF"),
		},
	}

	testCases[0].upf.EstablishAssociation(context.Background())

	Convey("AddFAR should indeed add FAR and report error appropiately", t, func() {
		for i, testcase := range testCases {
			upf := testcase.upf
			infoStr := fmt.Sprintf("testcase[%d]: ", i)

			Convey(infoStr, func() {
				_, err := upf.AddFAR()

				Convey(testcase.resultStr, func() {
					if testcase.expectedError == nil {
						So(err, ShouldBeNil)
					} else {
						So(err, ShouldNotBeNil)
						if err != nil {
							So(err.Error(), ShouldEqual, testcase.expectedError.Error())
						}
					}
				})
			})
		}
	})
}

func TestAddQER(t *testing.T) {
	testCases := []struct {
		upf           *smf_context.UPF
		resultStr     string
		expectedError error
	}{
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddQER should success",
			expectedError: nil,
		},
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddQER should fail",
			expectedError: fmt.Errorf("UPF[127.0.0.1] not associated with SMF"),
		},
	}

	testCases[0].upf.EstablishAssociation(context.Background())

	Convey("AddQER should indeed add QER and report error appropiately", t, func() {
		for i, testcase := range testCases {
			upf := testcase.upf
			infoStr := fmt.Sprintf("testcase[%d]: ", i)

			Convey(infoStr, func() {
				_, err := upf.AddQER()

				Convey(testcase.resultStr, func() {
					if testcase.expectedError == nil {
						So(err, ShouldBeNil)
					} else {
						So(err, ShouldNotBeNil)
						if err != nil {
							So(err.Error(), ShouldEqual, testcase.expectedError.Error())
						}
					}
				})
			})
		}
	})
}

func TestAddBAR(t *testing.T) {
	testCases := []struct {
		upf           *smf_context.UPF
		resultStr     string
		expectedError error
	}{
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddBAR should success",
			expectedError: nil,
		},
		{
			upf:           smf_context.NewUPF(mockIPv4NodeID, mockIfaces),
			resultStr:     "AddBAR should fail",
			expectedError: fmt.Errorf("UPF[127.0.0.1] not associated with SMF"),
		},
	}

	testCases[0].upf.EstablishAssociation(context.Background())

	Convey("AddBAR should indeed add BAR and report error appropiately", t, func() {
		for i, testcase := range testCases {
			upf := testcase.upf
			infoStr := fmt.Sprintf("testcase[%d]: ", i)

			Convey(infoStr, func() {
				_, err := upf.AddBAR()

				Convey(testcase.resultStr, func() {
					if testcase.expectedError == nil {
						So(err, ShouldBeNil)
					} else {
						So(err, ShouldNotBeNil)
						if err != nil {
							So(err.Error(), ShouldEqual, testcase.expectedError.Error())
						}
					}
				})
			})
		}
	})
}

func TestUPFAssociationStateLifecycle(t *testing.T) {
	upf := smf_context.NewUPF(mockIPv4NodeID, mockIfaces)
	if got := upf.AssociationState(); got != smf_context.AssociationDown {
		t.Fatalf("initial AssociationState() = %s, want down", got)
	}
	if err := upf.IsAssociated(); err == nil {
		t.Fatal("new UPF unexpectedly reported an established association")
	}

	if !upf.BeginAssociationSetup() {
		t.Fatal("first BeginAssociationSetup() was rejected")
	}
	if got := upf.AssociationState(); got != smf_context.AssociationSettingUp {
		t.Fatalf("AssociationState() = %s, want setting-up", got)
	}
	if upf.BeginAssociationSetup() {
		t.Fatal("concurrent BeginAssociationSetup() was accepted")
	}
	upf.FailAssociationSetup()
	if got := upf.AssociationState(); got != smf_context.AssociationDown {
		t.Fatalf("state after failed setup = %s, want down", got)
	}

	if !upf.BeginAssociationSetup() {
		t.Fatal("second BeginAssociationSetup() was rejected")
	}
	parent, cancelParent := context.WithCancel(context.Background())
	upf.EstablishAssociation(parent)
	t.Cleanup(upf.CancelAssociation)
	if got := upf.AssociationState(); got != smf_context.AssociationEstablished {
		t.Fatalf("state after setup = %s, want established", got)
	}
	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("established UPF reported not associated: %v", err)
	}

	if !upf.BeginAssociationRelease() {
		t.Fatal("BeginAssociationRelease() was rejected")
	}
	if got := upf.AssociationState(); got != smf_context.AssociationReleasing {
		t.Fatalf("state during release = %s, want releasing", got)
	}
	if err := upf.IsAssociated(); err != nil {
		t.Fatalf("releasing UPF must remain associated for Session Deletion: %v", err)
	}
	if err := upf.IsAvailable(); err == nil {
		t.Fatal("releasing UPF remained available for new session work")
	}
	if upf.BeginAssociationRelease() {
		t.Fatal("concurrent BeginAssociationRelease() was accepted")
	}

	associationDone := upf.AssociationDone()
	cancelParent()
	select {
	case <-associationDone:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not close AssociationDone")
	}
	deadline := time.Now().Add(time.Second)
	for upf.AssociationState() != smf_context.AssociationDown && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := upf.AssociationState(); got != smf_context.AssociationDown {
		t.Fatalf("state after parent cancellation = %s, want down", got)
	}
}

func TestUPFAssociationReleaseWaitsForInFlightSessionWork(t *testing.T) {
	upf := smf_context.NewUPF(mockIPv4NodeID, mockIfaces)
	upf.EstablishAssociation(context.Background())
	t.Cleanup(upf.CancelAssociation)

	associationContext, finishSessionWork, err := upf.BeginSessionWork()
	if err != nil {
		t.Fatalf("begin session work: %v", err)
	}
	if associationContext == nil {
		t.Fatal("BeginSessionWork returned a nil association context")
	}

	associationReleaseStarted := make(chan bool, 1)
	go func() {
		associationReleaseStarted <- upf.BeginAssociationRelease()
	}()

	select {
	case <-associationReleaseStarted:
		t.Fatal("Association Release started before in-flight session work finished")
	case <-time.After(20 * time.Millisecond):
	}
	if !upf.IsAssociationReleasing() {
		t.Fatal("UPF remained available while Association Release waited for in-flight work")
	}
	if err = upf.IsAvailable(); err == nil {
		t.Fatal("UPF reported available while Association Release waited for in-flight work")
	}

	finishSessionWork()
	select {
	case started := <-associationReleaseStarted:
		if !started {
			t.Fatal("first Association Release attempt was unexpectedly rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("Association Release did not start after in-flight session work finished")
	}

	if _, _, err = upf.BeginSessionWork(); err == nil {
		t.Fatal("new session work was accepted while Association Release was in progress")
	}

	upf.CancelAssociation()
	if !upf.BeginAssociationSetup() {
		t.Fatal("Association Setup after release was rejected")
	}
	upf.EstablishAssociation(context.Background())
	associationContext, finishSessionWork, err = upf.BeginSessionWork()
	if err != nil {
		t.Fatalf("session work was not restored after Association Release ended: %v", err)
	}
	if associationContext == nil {
		t.Fatal("restored Session work returned a nil association context")
	}
	finishSessionWork()
}

func TestUPFRecoveryTimeStampConcurrentAccess(t *testing.T) {
	nodeID := pfcptype.NodeID{
		NodeIdType: pfcptype.NodeIdTypeIpv4Address,
		IP:         net.ParseIP("192.0.2.110").To4(),
	}
	upf := smf_context.NewUPF(&nodeID, nil)
	t.Cleanup(func() { smf_context.RemoveUPFNodeByNodeID(nodeID) })

	baseline := time.Now().Add(-time.Hour).Truncate(time.Second)
	if !upf.AcceptRecoveryTimeStamp(baseline) {
		t.Fatal("first recovery timestamp was rejected")
	}
	if got := upf.RecoveryTimeStamp(); !got.Equal(baseline) {
		t.Fatalf("RecoveryTimeStamp() = %v, want %v", got, baseline)
	}
	if upf.AcceptRecoveryTimeStamp(baseline.Add(time.Second)) {
		t.Fatal("newer recovery timestamp did not signal a UPF restart")
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		recoveryTime := baseline.Add(time.Duration(i) * time.Second)
		wg.Add(3)
		go func() {
			defer wg.Done()
			upf.SetRecoveryTimeStamp(recoveryTime)
		}()
		go func() {
			defer wg.Done()
			_ = upf.RecoveryTimeStamp()
		}()
		go func() {
			defer wg.Done()
			_ = upf.AcceptRecoveryTimeStamp(recoveryTime)
		}()
	}
	wg.Wait()
}
