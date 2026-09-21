package consumer

import (
	"context"
	"sync"

	"github.com/free5gc/openapi/models"
)

// nrfRegistrar adapts nnrfService to nfheartbeat.Registrar.
type nrfRegistrar struct {
	s *nnrfService
}

func (r nrfRegistrar) UpdateNFInstance(ctx context.Context, patchItems []models.PatchItem) (
	models.Nrf_NFMgmt_NFProfile, *models.ProblemDetails, error,
) {
	return r.s.SendUpdateNFInstance(ctx, patchItems)
}

func (r nrfRegistrar) RegisterNFInstance(ctx context.Context) (int32, error) {
	if err := r.s.RegisterNFInstance(ctx, false); err != nil {
		return 0, err
	}
	// Written by processRegisterResponse on this goroutine.
	return r.s.heartbeatTimer, nil
}

// StartHeartbeat launches the periodic NF heartbeat toward the NRF.
// It must be called after a successful NF registration.
func (s *nnrfService) StartHeartbeat(ctx context.Context, wg *sync.WaitGroup) {
	s.heartbeat.Start(ctx, wg, s.heartbeatTimer)
}

// WaitHeartbeatStopped blocks until the heartbeat goroutine has exited, so that
// no heartbeat PATCH or re-registration PUT can reach the NRF after
// deregistration. It returns immediately when the heartbeat was never started.
func (s *nnrfService) WaitHeartbeatStopped() {
	s.heartbeat.Wait()
}
