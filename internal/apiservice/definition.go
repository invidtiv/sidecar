package apiservice

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func xmlText(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
func unitText(value string) string {
	// systemd expands % specifiers and $ variables even inside quoted argv.
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func (n *Native) definition() []byte {
	o := n.options
	args := []string{o.Executable, "-config", o.ConfigPath, "api", "serve"}
	// Capture only runtime plumbing, never inherited secrets or Sidecar identity.
	env := map[string]string{"PATH": o.Path, "XDG_STATE_HOME": filepath.Dir(o.StateDir), ActivationRequired: "1"}
	if o.OS == "darwin" {
		var b strings.Builder
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
		fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n<key>ProgramArguments</key><array>", Label)
		for _, arg := range args {
			fmt.Fprintf(&b, "<string>%s</string>", xmlText(arg))
		}
		fmt.Fprintf(&b, "</array>\n<key>Sockets</key><dict><key>browser</key><dict><key>SockNodeName</key><string>127.0.0.1</string><key>SockServiceName</key><string>7861</string><key>SockFamily</key><string>IPv4</string><key>SockType</key><string>stream</string></dict><key>local</key><dict><key>SockPathName</key><string>%s</string><key>SockPathMode</key><integer>384</integer><key>SockType</key><string>stream</string></dict></dict>\n", xmlText(filepath.Join(o.StateDir, "api", "api.sock")))
		b.WriteString("<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n<key>ThrottleInterval</key><integer>5</integer>\n<key>ExitTimeOut</key><integer>10</integer>\n<key>EnvironmentVariables</key><dict>")
		keys := []string{"PATH", "XDG_STATE_HOME", ActivationRequired}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "<key>%s</key><string>%s</string>", key, xmlText(env[key]))
		}
		fmt.Fprintf(&b, "</dict>\n<key>StandardOutPath</key><string>%s</string>\n<key>StandardErrorPath</key><string>%s</string>\n</dict></plist>\n", xmlText(n.log), xmlText(n.log))
		return []byte(b.String())
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Sidecar UI API\nRequires=sidecar-api.socket\nAfter=sidecar-api.socket\n\n[Service]\nType=simple\nExecStart=")
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(unitText(strings.ReplaceAll(arg, "$", "$$")))
	}
	fmt.Fprintf(&b, "\nEnvironment=%s\nEnvironment=%s\nEnvironment=SIDECAR_API_SOCKET_ACTIVATION=1\nSockets=sidecar-api.socket\nRestart=always\nRestartSec=5\nTimeoutStopSec=10\n\n[Install]\nWantedBy=default.target\n", unitText("PATH="+env["PATH"]), unitText("XDG_STATE_HOME="+env["XDG_STATE_HOME"]))
	return []byte(b.String())
}

// A single Accept=no unit keeps both sockets open while the service restarts.
// Address validation classifies its descriptors, which share one fd name.
func (n *Native) socketDefinition() []byte {
	return []byte(fmt.Sprintf("[Unit]\nDescription=Sidecar UI API sockets\n\n[Socket]\nListenStream=127.0.0.1:7861\nListenStream=%s\nSocketMode=0600\nDirectoryMode=0700\nFileDescriptorName=sidecar-api\nService=sidecar-api.service\nAccept=no\nRemoveOnStop=true\n\n[Install]\nWantedBy=sockets.target\n", strings.ReplaceAll(filepath.Join(n.options.StateDir, "api", "api.sock"), "%", "%%")))
}
