//go:build linux

package notifydelivery

import "os"

func NewPlatformNative(runner Runner) NativeNotifier {
	return newLinuxNative(runner, os.Getenv, providerTimeout)
}

func NewPlatformSound(runner Runner, cache AssetCache) SoundPlayer {
	return newLinuxSound(runner, cache, providerTimeout)
}
