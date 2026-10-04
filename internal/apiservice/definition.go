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
	env := map[string]string{"PATH": o.Path, "XDG_STATE_HOME": filepath.Dir(o.StateDir)}
	if o.OS == "darwin" {
		var b strings.Builder
		b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n")
		fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n<key>ProgramArguments</key><array>", Label)
		for _, arg := range args {
			fmt.Fprintf(&b, "<string>%s</string>", xmlText(arg))
		}
		b.WriteString("</array>\n<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n<key>ThrottleInterval</key><integer>5</integer>\n<key>ExitTimeOut</key><integer>10</integer>\n<key>EnvironmentVariables</key><dict>")
		keys := []string{"PATH", "XDG_STATE_HOME"}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "<key>%s</key><string>%s</string>", key, xmlText(env[key]))
		}
		fmt.Fprintf(&b, "</dict>\n<key>StandardOutPath</key><string>%s</string>\n<key>StandardErrorPath</key><string>%s</string>\n</dict></plist>\n", xmlText(n.log), xmlText(n.log))
		return []byte(b.String())
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Sidecar UI API\n\n[Service]\nType=simple\nExecStart=")
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(unitText(strings.ReplaceAll(arg, "$", "$$")))
	}
	fmt.Fprintf(&b, "\nEnvironment=%s\nEnvironment=%s\nRestart=always\nRestartSec=5\nTimeoutStopSec=10\n\n[Install]\nWantedBy=default.target\n", unitText("PATH="+env["PATH"]), unitText("XDG_STATE_HOME="+env["XDG_STATE_HOME"]))
	return []byte(b.String())
}
