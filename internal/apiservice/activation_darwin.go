package apiservice

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// launch_activate_socket is a libSystem API, not a syscall. purego keeps the
// release's CGO_ENABLED=0 and cross-build support without a helper executable.
func launchdListeners() ([]ActivatedListener, error) {
	library, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("socket activation: load libSystem: %w", err)
	}
	defer func() { _ = purego.Dlclose(library) }()
	symbol, err := purego.Dlsym(library, "launch_activate_socket")
	if err != nil {
		return nil, err
	}
	var activate func(string, **int32, *uintptr) int32
	purego.RegisterFunc(&activate, symbol)
	symbol, err = purego.Dlsym(library, "free")
	if err != nil {
		return nil, err
	}
	var free func(unsafe.Pointer)
	purego.RegisterFunc(&free, symbol)
	return collectLaunchd(func(name string) ([]int, error) {
		var fds *int32
		var count uintptr
		code := activate(name, &fds, &count)
		if code != 0 {
			return nil, syscall.Errno(code)
		}
		defer free(unsafe.Pointer(fds))
		// The plist restricts each entry to one socket; collectLaunchd closes any
		// unexpected extra descriptors before refusing it.
		descriptors := unsafe.Slice(fds, count)
		result := make([]int, len(descriptors))
		for i, fd := range descriptors {
			result[i] = int(fd)
		}
		return result, nil
	})
}

func collectLaunchd(activate func(string) ([]int, error)) (listeners []ActivatedListener, err error) {
	defer func() {
		if err != nil {
			CloseActivated(listeners)
			listeners = nil
		}
	}()
	for _, name := range []string{"browser", "local", "tailnet"} {
		fds, activationErr := activate(name)
		if errors.Is(activationErr, syscall.ESRCH) {
			return listeners, nil
		}
		if errors.Is(activationErr, syscall.ENOENT) {
			continue
		}
		if activationErr != nil {
			return listeners, fmt.Errorf("activate %s: %w", name, activationErr)
		}
		files := make([]*os.File, len(fds))
		for i, fd := range fds {
			syscall.CloseOnExec(fd)
			files[i] = os.NewFile(uintptr(fd), name)
		}
		if len(files) != 1 {
			for _, file := range files {
				_ = file.Close()
			}
			return listeners, fmt.Errorf("socket activation: %s must supply exactly one listener", name)
		}
		listener, convertErr := listenerFromFile(files[0])
		_ = files[0].Close()
		if convertErr != nil {
			return listeners, convertErr
		}
		listeners = append(listeners, ActivatedListener{Name: name, Listener: listener})
	}
	return listeners, nil
}
