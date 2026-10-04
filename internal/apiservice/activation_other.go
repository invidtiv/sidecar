//go:build !darwin

package apiservice

func launchdListeners() ([]ActivatedListener, error) { return nil, nil }
