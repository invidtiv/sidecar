package notifydelivery

import (
	"context"
	"sync"
	"time"
)

type audioRequest struct {
	ctx  context.Context
	cue  Cue
	done chan audioResult
}

type audioResult struct {
	receipt ProviderReceipt
	err     error
}

// AudioArbiter collapses a burst to one highest-priority cue and never overlaps
// an in-flight player. While one cue plays, every arrival becomes at most one
// next batch, so a noisy transition burst cannot grow into a long queue.
type AudioArbiter struct {
	player SoundPlayer
	window time.Duration
	clock  Clock

	mu      sync.Mutex
	pending []audioRequest
	timer   Timer
	playing bool
}

var _ SoundPlayer = (*AudioArbiter)(nil)

func NewAudioArbiter(player SoundPlayer, window time.Duration, clock Clock) *AudioArbiter {
	if window < 0 {
		window = 0
	}
	if clock == nil {
		clock = RealClock{}
	}
	return &AudioArbiter{player: player, window: window, clock: clock}
}

func (a *AudioArbiter) Probe(ctx context.Context) Capability {
	if a == nil || a.player == nil {
		return Capability{Reason: "no sound player"}
	}
	return a.player.Probe(ctx)
}

func (a *AudioArbiter) Play(ctx context.Context, cue Cue) (ProviderReceipt, error) {
	request := audioRequest{ctx: ctx, cue: cue, done: make(chan audioResult, 1)}
	a.mu.Lock()
	a.pending = append(a.pending, request)
	if !a.playing && a.timer == nil {
		a.timer = a.clock.AfterFunc(a.window, a.flush)
	}
	a.mu.Unlock()
	select {
	case result := <-request.done:
		return result.receipt, result.err
	case <-ctx.Done():
		a.mu.Lock()
		for i := range a.pending {
			if a.pending[i].done == request.done {
				a.pending = append(a.pending[:i], a.pending[i+1:]...)
				break
			}
		}
		a.mu.Unlock()
		return ProviderReceipt{}, ctx.Err()
	}
}

func (a *AudioArbiter) flush() {
	a.mu.Lock()
	if a.playing || len(a.pending) == 0 {
		a.timer = nil
		a.mu.Unlock()
		return
	}
	batch := a.pending
	a.pending = nil
	a.timer = nil
	a.playing = true
	a.mu.Unlock()

	winner := 0
	for i := 1; i < len(batch); i++ {
		if batch[i].cue.priority() > batch[winner].cue.priority() {
			winner = i
		}
	}
	receipt, err := a.player.Play(batch[winner].ctx, batch[winner].cue)
	batch[winner].done <- audioResult{receipt: receipt, err: err}
	for i := range batch {
		if i == winner {
			continue
		}
		batch[i].done <- audioResult{receipt: ProviderReceipt{
			Provider: receipt.Provider, Delivered: false,
			Reason: string(notifyReasonRateLimited), At: a.clock.Now().UTC(),
		}}
	}

	a.mu.Lock()
	a.playing = false
	if len(a.pending) > 0 && a.timer == nil {
		a.timer = a.clock.AfterFunc(a.window, a.flush)
	}
	a.mu.Unlock()
}

const notifyReasonRateLimited = "rate_limited"
