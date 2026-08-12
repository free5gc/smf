package context

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/free5gc/openapi/models"
)

var (
	ErrEventExposureNoMatchingSession = errors.New("event exposure no active matching session")
	ErrEventExposureMissingUEIP       = errors.New("event exposure missing UE IP")
	ErrEventExposureMissingUPF        = errors.New("event exposure missing selected UPF")
	ErrEventExposureMultipleSessions  = errors.New("event exposure multiple matching sessions")
	ErrEventExposureMissingEndpoint   = errors.New("event exposure selected UPF missing endpoint")
)

type EventExposureTargetResolver struct{}

func NewEventExposureTargetResolver() *EventExposureTargetResolver {
	return &EventExposureTargetResolver{}
}

func (r *EventExposureTargetResolver) ResolveEventExposureTarget(
	_ context.Context,
	supi string,
	selectors EventExposureSelectors,
) (EventExposureTarget, error) {
	matches := make([]eventExposureSessionSnapshot, 0, 1)
	var missingUEIP bool
	var missingUPF bool

	smContextPool.Range(func(_, value interface{}) bool {
		smContext, ok := value.(*SMContext)
		if !ok {
			return true
		}

		snapshot, match := snapshotEventExposureSession(smContext, supi, selectors)
		if !match {
			return true
		}
		if snapshot.ueIP == nil {
			missingUEIP = true
			return true
		}
		if snapshot.selectedUPF == nil {
			missingUPF = true
			return true
		}
		matches = append(matches, snapshot)
		return true
	})

	if len(matches) == 0 {
		switch {
		case missingUEIP:
			return EventExposureTarget{}, ErrEventExposureMissingUEIP
		case missingUPF:
			return EventExposureTarget{}, ErrEventExposureMissingUPF
		default:
			return EventExposureTarget{}, ErrEventExposureNoMatchingSession
		}
	}
	if len(matches) > 1 {
		return EventExposureTarget{}, ErrEventExposureMultipleSessions
	}

	return resolveEventExposureUPFSnapshot(matches[0])
}

type eventExposureSessionSnapshot struct {
	selectedUPF  *UPNode
	ueIP         net.IP
	dnn          string
	snssai       *modelsSnssai
	pduSessionID int32
}

type modelsSnssai struct {
	sst int32
	sd  string
}

func snapshotEventExposureSession(
	smContext *SMContext,
	supi string,
	selectors EventExposureSelectors,
) (eventExposureSessionSnapshot, bool) {
	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	if smContext.State() != Active || smContext.Supi != supi {
		return eventExposureSessionSnapshot{}, false
	}
	if selectors.PDUSessionID != nil && smContext.PDUSessionID != *selectors.PDUSessionID {
		return eventExposureSessionSnapshot{}, false
	}
	if selectors.Dnn != nil && smContext.Dnn != *selectors.Dnn {
		return eventExposureSessionSnapshot{}, false
	}
	if selectors.Snssai != nil {
		if !eventExposureSnssaiEqual(smContext.SNssai, selectors.Snssai) {
			return eventExposureSessionSnapshot{}, false
		}
	}

	var snssai *modelsSnssai
	if smContext.SNssai != nil {
		snssai = &modelsSnssai{sst: smContext.SNssai.Sst, sd: smContext.SNssai.Sd}
	}
	return eventExposureSessionSnapshot{
		selectedUPF:  smContext.SelectedUPF,
		ueIP:         append(net.IP(nil), smContext.PDUAddress...),
		dnn:          smContext.Dnn,
		snssai:       snssai,
		pduSessionID: smContext.PDUSessionID,
	}, true
}

func eventExposureSnssaiEqual(a, b *models.Snssai) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Sst == b.Sst && strings.EqualFold(a.Sd, b.Sd)
}

func resolveEventExposureUPFSnapshot(snapshot eventExposureSessionSnapshot) (EventExposureTarget, error) {
	upi := GetUserPlaneInformation()
	if upi == nil {
		return EventExposureTarget{}, ErrEventExposureMissingUPF
	}

	upi.Mu.RLock()
	defer upi.Mu.RUnlock()

	for name, upfNode := range upi.UPFs {
		if upfNode != snapshot.selectedUPF {
			continue
		}
		if upfNode.UPF == nil {
			return EventExposureTarget{}, ErrEventExposureMissingUPF
		}
		if upfNode.NupfEeApiRoot == "" {
			return EventExposureTarget{}, ErrEventExposureMissingEndpoint
		}
		target := EventExposureTarget{
			UPFName:        name,
			UPFID:          upfNode.UPF.UUID(),
			APIroot:        upfNode.NupfEeApiRoot,
			ServiceBaseURL: upfNode.NupfEeApiRoot + "/nupf-ee/v1",
			UEIPAddress:    append(net.IP(nil), snapshot.ueIP...),
			Dnn:            snapshot.dnn,
			PDUSessionID:   snapshot.pduSessionID,
		}
		if snapshot.snssai != nil {
			target.Snssai = &models.Snssai{Sst: snapshot.snssai.sst, Sd: snapshot.snssai.sd}
		}
		return target, nil
	}

	return EventExposureTarget{}, ErrEventExposureMissingUPF
}
