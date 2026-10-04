package apiservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func (n *Native) Status(ctx context.Context) (Status, error) {
	manager := "launchd"
	if n.options.OS == "linux" {
		manager = "systemd"
	}
	status := Status{Manager: manager, Label: n.label, File: n.file, Log: n.log}
	_, err := os.Stat(n.file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return status, fmt.Errorf("read service definition: %w; check permissions on %s", err, n.file)
	}
	status.Installed = err == nil
	status.Socket.File = n.file
	if n.options.OS == "darwin" && status.Installed {
		data, readErr := os.ReadFile(n.file)
		if readErr != nil {
			return status, readErr
		}
		status.Socket.Installed = strings.Contains(string(data), "<key>Sockets</key>") && strings.Contains(string(data), "<key>browser</key>")
	}
	if n.options.OS == "linux" {
		status.Socket.File = n.socketFile
		_, socketErr := os.Stat(n.socketFile)
		if socketErr != nil && !errors.Is(socketErr, os.ErrNotExist) {
			return status, socketErr
		}
		status.Socket.Installed = socketErr == nil
	}
	if n.options.OS == "darwin" {
		output, err := n.command(ctx, "print", n.target())
		if err != nil {
			if !strings.Contains(string(output), "Could not find service") {
				return status, err
			}
		} else {
			status.Loaded = true
			parseLaunchd(string(output), &status)
			status.Socket.Loaded = strings.Contains(string(output), "sockets = {") && strings.Contains(string(output), "browser = {")
			status.Socket.Listening = status.Socket.Loaded
		}
	} else {
		output, err := n.command(ctx, "--user", "show", Unit, "--property=LoadState,ActiveState,MainPID,ExecMainCode,ExecMainStatus")
		values := parseProperties(string(output))
		if err != nil && values["LoadState"] != "not-found" {
			return status, err
		}
		if values["LoadState"] == "" {
			return status, errors.New("systemd returned no LoadState; run `systemctl --user status sidecar-api.service` and retry")
		}
		status.Loaded = values["LoadState"] == "loaded"
		status.PID, _ = strconv.Atoi(values["MainPID"])
		status.Running = values["ActiveState"] == "active" && status.PID > 0
		code, _ := strconv.Atoi(values["ExecMainCode"])
		if code != 0 {
			exit, _ := strconv.Atoi(values["ExecMainStatus"])
			status.LastExit = &Exit{Code: exit}
			if code == 2 || code == 3 {
				status.LastExit = &Exit{Signal: strconv.Itoa(exit)}
			}
		}
		output, err = n.command(ctx, "--user", "show", SocketUnit, "--property=LoadState,ActiveState,SubState")
		values = parseProperties(string(output))
		if err != nil && values["LoadState"] != "not-found" {
			return status, err
		}
		if values["LoadState"] == "" {
			return status, errors.New("systemd returned no socket LoadState; inspect `systemctl --user status sidecar-api.socket`")
		}
		status.Socket.Loaded = values["LoadState"] == "loaded"
		status.Socket.Listening = status.Socket.Loaded && values["ActiveState"] == "active" && (values["SubState"] == "listening" || values["SubState"] == "running")
	}
	if !status.Running {
		status.PID = 0
	}
	switch {
	case status.Running && !status.Socket.Listening:
		status.Message = "API service is running without a manager-held browser socket; run `sidecar api service install` to protect restart windows."
	case status.Running:
		status.Message = "API service is running; open it with `sidecar api open`."
	case !status.Installed && !status.Loaded:
		status.Message = "API service is not installed; run `sidecar api service install`."
	default:
		status.Message = "API service is not running; inspect " + status.Log + ", then run `sidecar api service install`."
	}
	return status, nil
}

func parseLaunchd(output string, status *Status) {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			status.PID, _ = strconv.Atoi(value)
		case "state":
			status.Running = value == "running"
		case "last exit code":
			code, err := strconv.Atoi(value)
			if err == nil {
				status.LastExit = &Exit{Code: code}
			}
		case "last terminating signal":
			status.LastExit = &Exit{Signal: value}
		}
	}
	status.Running = status.Running && status.PID > 0
}
func parseProperties(output string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
