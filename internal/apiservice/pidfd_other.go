//go:build !linux

package apiservice

import "errors"

func matchesPIDFD(string) (bool, error) {
	return false, errors.New("socket activation: LISTEN_PIDFDID is only supported on Linux")
}
