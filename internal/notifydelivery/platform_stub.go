//go:build !darwin && !linux

package notifydelivery

import "context"

type unavailableNative struct{}

func NewPlatformNative(Runner) NativeNotifier { return unavailableNative{} }
func (unavailableNative) Probe(context.Context) Capability {
	return Capability{Reason: "native notification delivery is not available on this platform yet"}
}
func (unavailableNative) Deliver(context.Context, Message) (ProviderReceipt, error) {
	return ProviderReceipt{}, ErrUnsupported
}
func (unavailableNative) Remove(context.Context, string) error { return ErrUnsupported }

type unavailableSound struct{}

func NewPlatformSound(Runner, AssetCache) SoundPlayer { return unavailableSound{} }
func (unavailableSound) Probe(context.Context) Capability {
	return Capability{Reason: "sound delivery is not available on this platform yet"}
}
func (unavailableSound) Play(context.Context, Cue) (ProviderReceipt, error) {
	return ProviderReceipt{}, ErrUnsupported
}
