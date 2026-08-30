package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
	"github.com/marcus/sidecar/internal/uirequest"
)

// notifyWait is how long a post or dismiss waits for a running instance to
// acknowledge before falling back to writing the log directly. It is shorter
// than `open`'s because nothing is being placed on screen for the caller to
// look at: the notification is filed either way.
const notifyWait = 900 * time.Millisecond

func runNotifyRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("notify")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown notify command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

func runNotifyConfig(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("notify").FindSubcommand("config")
	if len(args) > 0 && args[0] == "set" {
		return runNotifyConfigSet(env, args[1:])
	}
	jsonOutput := false
	for _, arg := range args {
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
			return 0
		case arg == "--json":
			jsonOutput = true
		default:
			cliErrf(env.Stderr, "unknown notify config option %q\n\n%s", arg, RenderHelp(cmd))
			return 2
		}
	}
	cfg, err := loadAndApplyNotificationConfig()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return writeNotificationConfig(env, cfg.Notifications, jsonOutput)
}

func runNotifyConfigSet(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("notify").FindSubcommand("config").FindSubcommand("set")
	help := RenderHelp(cmd)
	jsonOutput := false
	var nativeMode, soundMode, quietHours string
	var attentionPath, donePath, failurePath string
	setNative, setSound, setQuiet := false, false, false
	setAttentionPath, setDonePath, setFailurePath := false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func(flag string) (string, bool) {
			if strings.HasPrefix(arg, flag+"=") {
				return strings.TrimPrefix(arg, flag+"="), true
			}
			if arg == flag && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--native" || strings.HasPrefix(arg, "--native="):
			v, ok := value("--native")
			if !ok {
				cliErrf(env.Stderr, "--native requires off, background, or always\n\n%s", help)
				return 2
			}
			nativeMode, setNative = v, true
		case arg == "--sound" || strings.HasPrefix(arg, "--sound="):
			v, ok := value("--sound")
			if !ok {
				cliErrf(env.Stderr, "--sound requires off, background, or always\n\n%s", help)
				return 2
			}
			soundMode, setSound = v, true
		case arg == "--quiet-hours" || strings.HasPrefix(arg, "--quiet-hours="):
			v, ok := value("--quiet-hours")
			if !ok {
				cliErrf(env.Stderr, "--quiet-hours requires off or HH:MM-HH:MM\n\n%s", help)
				return 2
			}
			quietHours, setQuiet = v, true
		case arg == "--attention-path" || strings.HasPrefix(arg, "--attention-path="):
			v, ok := value("--attention-path")
			if !ok {
				cliErrf(env.Stderr, "--attention-path requires a file path (use --attention-path= to restore the built-in sound)\n\n%s", help)
				return 2
			}
			attentionPath, setAttentionPath = v, true
		case arg == "--done-path" || strings.HasPrefix(arg, "--done-path="):
			v, ok := value("--done-path")
			if !ok {
				cliErrf(env.Stderr, "--done-path requires a file path (use --done-path= to restore the built-in sound)\n\n%s", help)
				return 2
			}
			donePath, setDonePath = v, true
		case arg == "--failure-path" || strings.HasPrefix(arg, "--failure-path="):
			v, ok := value("--failure-path")
			if !ok {
				cliErrf(env.Stderr, "--failure-path requires a file path (use --failure-path= to restore the built-in sound)\n\n%s", help)
				return 2
			}
			failurePath, setFailurePath = v, true
		default:
			cliErrf(env.Stderr, "unknown notify config set option %q\n\n%s", arg, help)
			return 2
		}
	}
	if !setNative && !setSound && !setQuiet && !setAttentionPath && !setDonePath && !setFailurePath {
		cliErrf(env.Stderr, "notify config set requires at least one setting\n\n%s", help)
		return 2
	}
	prospective, err := loadAndApplyNotificationConfig()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if setNative {
		prospective.Notifications.Native.Mode = config.DeliveryMode(nativeMode)
	}
	if setSound {
		prospective.Notifications.Sound.Mode = config.DeliveryMode(soundMode)
	}
	if setQuiet {
		enabled, start, end, parseErr := parseQuietHoursSetting(quietHours)
		if parseErr != nil {
			cliErrln(env.Stderr, parseErr)
			return 2
		}
		prospective.Notifications.QuietHours.Enabled = enabled
		if enabled {
			prospective.Notifications.QuietHours.Start = start
			prospective.Notifications.QuietHours.End = end
		}
	}
	if setAttentionPath {
		prospective.Notifications.Sound.AttentionPath = strings.TrimSpace(attentionPath)
	}
	if setDonePath {
		prospective.Notifications.Sound.DonePath = strings.TrimSpace(donePath)
	}
	if setFailurePath {
		prospective.Notifications.Sound.FailurePath = strings.TrimSpace(failurePath)
	}
	if err := config.ValidateNotifications(prospective.Notifications, config.ConfigPath()); err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	err = config.SaveNotifications(func(cfg *config.NotificationsConfig) {
		if setNative {
			cfg.Native.Mode = config.DeliveryMode(nativeMode)
		}
		if setSound {
			cfg.Sound.Mode = config.DeliveryMode(soundMode)
		}
		if setQuiet {
			cfg.QuietHours = prospective.Notifications.QuietHours
		}
		if setAttentionPath {
			cfg.Sound.AttentionPath = prospective.Notifications.Sound.AttentionPath
		}
		if setDonePath {
			cfg.Sound.DonePath = prospective.Notifications.Sound.DonePath
		}
		if setFailurePath {
			cfg.Sound.FailurePath = prospective.Notifications.Sound.FailurePath
		}
	})
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	cfg, err := loadAndApplyNotificationConfig()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	broadcastNotificationConfigReload(env)
	return writeNotificationConfig(env, cfg.Notifications, jsonOutput)
}

func parseQuietHoursSetting(raw string) (bool, string, string, error) {
	if raw == "off" {
		return false, "", "", nil
	}
	if len(raw) != len("HH:MM-HH:MM") || raw[5] != '-' {
		return false, "", "", fmt.Errorf("quiet hours must be off or HH:MM-HH:MM")
	}
	start, end := raw[:5], raw[6:]
	prospective := config.DefaultNotificationsConfig()
	prospective.QuietHours.Enabled = true
	prospective.QuietHours.Start = start
	prospective.QuietHours.End = end
	if err := config.ValidateNotifications(prospective, config.ConfigPath()); err != nil {
		return false, "", "", err
	}
	return true, start, end, nil
}

func runNotifySourceRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("notify").FindSubcommand("source")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	if args[0] == "set" {
		return runNotifySourceSet(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown notify source command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

func runNotifySourceSet(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("notify").FindSubcommand("source").FindSubcommand("set")
	help := RenderHelp(cmd)
	jsonOutput := false
	var sourceID string
	var toastValue, nativeValue, soundValue, expiryValue string
	setToast, setNative, setSound, setExpiry := false, false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func(flag string) (string, bool) {
			if strings.HasPrefix(arg, flag+"=") {
				return strings.TrimPrefix(arg, flag+"="), true
			}
			if arg == flag && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--toast" || strings.HasPrefix(arg, "--toast="):
			v, ok := value("--toast")
			if !ok {
				cliErrf(env.Stderr, "--toast requires on or off\n\n%s", help)
				return 2
			}
			toastValue, setToast = v, true
		case arg == "--native" || strings.HasPrefix(arg, "--native="):
			v, ok := value("--native")
			if !ok {
				cliErrf(env.Stderr, "--native requires on or off\n\n%s", help)
				return 2
			}
			nativeValue, setNative = v, true
		case arg == "--sound" || strings.HasPrefix(arg, "--sound="):
			v, ok := value("--sound")
			if !ok {
				cliErrf(env.Stderr, "--sound requires none, event, attention, done, or failure\n\n%s", help)
				return 2
			}
			soundValue, setSound = v, true
		case arg == "--expiry" || strings.HasPrefix(arg, "--expiry="):
			v, ok := value("--expiry")
			if !ok {
				cliErrf(env.Stderr, "--expiry requires a duration or sticky\n\n%s", help)
				return 2
			}
			expiryValue, setExpiry = v, true
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown notify source set option %q\n\n%s", arg, help)
			return 2
		case sourceID == "":
			sourceID = arg
		default:
			cliErrf(env.Stderr, "notify source set accepts exactly one source\n\n%s", help)
			return 2
		}
	}
	if !notify.ValidSource(notify.SourceID(sourceID)) {
		cliErrf(env.Stderr, "source must be one of %s\n\n%s", strings.Join(notify.SourceIDs(), ", "), help)
		return 2
	}
	if !setToast && !setNative && !setSound && !setExpiry {
		cliErrf(env.Stderr, "notify source set requires at least one setting\n\n%s", help)
		return 2
	}
	toast, err := parseOnOff("toast", toastValue, setToast)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	native, err := parseOnOff("native", nativeValue, setNative)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	prospective, err := loadAndApplyNotificationConfig()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	apply := func(cfg *config.NotificationsConfig) {
		if cfg.Sources == nil {
			cfg.Sources = map[string]config.NotificationSourceConfig{}
		}
		rule := cfg.Sources[sourceID]
		if setToast {
			rule.Toast = &toast
		}
		if setNative {
			rule.Native = &native
		}
		if setSound {
			rule.Sound = config.SoundCue(soundValue)
		}
		if setExpiry {
			rule.Expiry = expiryValue
		}
		cfg.Sources[sourceID] = rule
	}
	apply(&prospective.Notifications)
	if err := config.ValidateNotifications(prospective.Notifications, config.ConfigPath()); err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	if err := config.SaveNotifications(apply); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	cfg, err := loadAndApplyNotificationConfig()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	broadcastNotificationConfigReload(env)
	view := notificationConfigView(cfg.Notifications).Sources[sourceID]
	if jsonOutput {
		return writeJSON(env, struct {
			Source string                 `json:"source"`
			Rule   notificationSourceView `json:"rule"`
		}{Source: sourceID, Rule: view})
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s: toast %s, native %s, sound %s, expiry %s\n", sourceID, onOffText(view.Toast), onOffText(view.Native), view.Sound, view.Expiry)
	return 0
}

func parseOnOff(name, raw string, set bool) (bool, error) {
	if !set {
		return false, nil
	}
	switch raw {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("--%s must be on or off", name)
	}
}

func onOffText(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func writeNotificationConfig(env Env, cfg config.NotificationsConfig, jsonOutput bool) int {
	view := notificationConfigView(cfg)
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(view); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "System notifications: %s\nSounds: %s\n", notificationModeText(cfg.Native.Mode), notificationModeText(cfg.Sound.Mode))
	if cfg.QuietHours.Enabled {
		allDay := ""
		if cfg.QuietHours.Start == cfg.QuietHours.End {
			allDay = " (all day)"
		}
		_, _ = fmt.Fprintf(env.Stdout, "Quiet hours: %s-%s%s\n", cfg.QuietHours.Start, cfg.QuietHours.End, allDay)
	} else {
		_, _ = fmt.Fprintln(env.Stdout, "Quiet hours: off")
	}
	_, _ = fmt.Fprintln(env.Stdout, "Sound choices:")
	_, _ = fmt.Fprintln(env.Stdout, "  Attention: "+soundPathText(cfg.Sound.AttentionPath))
	_, _ = fmt.Fprintln(env.Stdout, "  Done: "+soundPathText(cfg.Sound.DonePath))
	_, _ = fmt.Fprintln(env.Stdout, "  Failure: "+soundPathText(cfg.Sound.FailurePath))
	_, _ = fmt.Fprintln(env.Stdout, "Source rules:")
	seen := make(map[string]bool, len(view.Sources))
	for _, source := range notify.Sources() {
		id := string(source.ID)
		writeNotificationSourceLine(env, id, view.Sources[id])
		seen[id] = true
	}
	unknown := make([]string, 0, len(view.Sources)-len(seen))
	for id := range view.Sources {
		if !seen[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	for _, id := range unknown {
		writeNotificationSourceLine(env, id, view.Sources[id])
	}
	return 0
}

func soundPathText(path string) string {
	if strings.TrimSpace(path) == "" {
		return "built-in"
	}
	return path
}

func writeNotificationSourceLine(env Env, id string, rule notificationSourceView) {
	_, _ = fmt.Fprintf(env.Stdout, "  %s: toast %s, native %s, sound %s, expiry %s\n", id, onOffText(rule.Toast), onOffText(rule.Native), rule.Sound, rule.Expiry)
}

type notificationSourceView struct {
	Toast  bool            `json:"toast"`
	Native bool            `json:"native"`
	Sound  config.SoundCue `json:"sound"`
	Expiry string          `json:"expiry"`
}

type notificationConfigResult struct {
	Native     config.NativeNotificationsConfig  `json:"native"`
	Sound      config.SoundNotificationsConfig   `json:"sound"`
	QuietHours config.QuietHoursConfig           `json:"quietHours"`
	Sources    map[string]notificationSourceView `json:"sources"`
}

func notificationConfigView(cfg config.NotificationsConfig) notificationConfigResult {
	resolved := notify.ResolveConfig(cfg)
	// Report the policy this build will actually apply. In particular, an
	// unrecognized hand-edited mode resolves to off in both human and JSON
	// output while the original spelling remains untouched on disk for repair.
	cfg.Native.Mode = resolved.NativeMode
	cfg.Sound.Mode = resolved.SoundMode
	sources := make(map[string]notificationSourceView)
	for id, rule := range resolved.SourceRules() {
		expiry := rule.Expiry.String()
		if rule.Expiry == 0 {
			expiry = "sticky"
		}
		sources[string(id)] = notificationSourceView{Toast: rule.Toast, Native: rule.Native, Sound: rule.Sound, Expiry: expiry}
	}
	// Unknown future sources are retained and reported even though this build
	// resolves them centre-only.
	for id, configured := range cfg.Sources {
		if _, known := sources[id]; known {
			continue
		}
		view := notificationSourceView{Toast: true, Sound: config.SoundNone, Expiry: notify.SourceOf(notify.SourceID(id)).DefaultExpiry.String()}
		if configured.Toast != nil {
			view.Toast = *configured.Toast
		}
		if configured.Native != nil {
			view.Native = *configured.Native
		}
		if configured.Sound != "" {
			view.Sound = configured.Sound
		}
		if configured.Expiry != "" {
			view.Expiry = configured.Expiry
		}
		sources[id] = view
	}
	return notificationConfigResult{Native: cfg.Native, Sound: cfg.Sound, QuietHours: cfg.QuietHours, Sources: sources}
}

// loadAndApplyNotificationConfig gives every fresh notify command the same
// persisted, resolved policy snapshot as the running app. This is deliberately
// done at command execution, after -config has selected the authoritative file.
func loadAndApplyNotificationConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	notify.ApplyConfig(cfg.Notifications)
	return cfg, nil
}

func notificationModeText(mode config.DeliveryMode) string {
	switch mode {
	case config.DeliveryBackground:
		return "background only"
	case config.DeliveryAlways:
		return "always"
	default:
		return "off"
	}
}

func runNotifyStatus(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("notify").FindSubcommand("status")
	jsonOutput := false
	for _, arg := range args {
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
			return 0
		case arg == "--json":
			jsonOutput = true
		default:
			cliErrf(env.Stderr, "unknown notify status option %q\n\n%s", arg, RenderHelp(cmd))
			return 2
		}
	}
	if _, err := loadAndApplyNotificationConfig(); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	status := notifydelivery.Status{
		Native: notifydelivery.Capability{Reason: "provider status unavailable"},
		Sound:  notifydelivery.Capability{Reason: "provider status unavailable"},
	}
	if provider, ok := env.NotificationDelivery.(notifydelivery.StatusProvider); ok {
		ctx := env.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		status = provider.Status(ctx)
	}
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(status); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprintln(env.Stdout, formatCapability("Native", status.Native))
	_, _ = fmt.Fprintln(env.Stdout, formatCapability("Sound", status.Sound))
	if status.Remote {
		_, _ = fmt.Fprintln(env.Stdout, "Context: remote shell")
	} else {
		_, _ = fmt.Fprintln(env.Stdout, "Context: local")
	}
	return 0
}

func formatCapability(label string, capability notifydelivery.Capability) string {
	if capability.Available {
		ready := label + ": ready"
		if capability.Provider != "" {
			ready += " (" + capability.Provider + ")"
		}
		if capability.Reason != "" {
			ready += "; warning: " + capability.Reason
		}
		return ready
	}
	reason := capability.Reason
	if reason == "" {
		reason = "unavailable"
	}
	return label + ": unavailable (" + reason + ")"
}

func runNotifyTest(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("notify").FindSubcommand("test")
	help := RenderHelp(cmd)
	jsonOutput := false
	channel := ""
	event := notifydelivery.TestWaiting
	source := notify.SourceID("")
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func(flag string) (string, bool) {
			if strings.HasPrefix(arg, flag+"=") {
				return strings.TrimPrefix(arg, flag+"="), true
			}
			if arg == flag && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--channel" || strings.HasPrefix(arg, "--channel="):
			v, ok := value("--channel")
			if !ok || (v != notifydelivery.ChannelNative && v != notifydelivery.ChannelSound && v != "all") {
				cliErrf(env.Stderr, "--channel requires native, sound, or all\n\n%s", help)
				return 2
			}
			if v != "all" {
				channel = v
			}
		case arg == "--event" || strings.HasPrefix(arg, "--event="):
			v, ok := value("--event")
			event = notifydelivery.TestEvent(v)
			if !ok || (event != notifydelivery.TestWaiting && event != notifydelivery.TestDone && event != notifydelivery.TestFailure) {
				cliErrf(env.Stderr, "--event requires waiting, done, or failure\n\n%s", help)
				return 2
			}
		case arg == "--source" || strings.HasPrefix(arg, "--source="):
			v, ok := value("--source")
			source = notify.SourceID(v)
			if !ok || !notify.ValidSource(source) {
				cliErrf(env.Stderr, "--source requires one of %s\n\n%s", strings.Join(notify.SourceIDs(), ", "), help)
				return 2
			}
		default:
			cliErrf(env.Stderr, "unknown notify test option %q\n\n%s", arg, help)
			return 2
		}
	}
	if channel == "" && !containsArg(args, "--channel") {
		cliErrf(env.Stderr, "notify test requires --channel native, sound, or all\n\n%s", help)
		return 2
	}
	if _, err := loadAndApplyNotificationConfig(); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	request, err := notifydelivery.ExplicitTestRequest(event)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	if source != "" {
		request.Notification.Source = source
	}
	request.Channel = channel
	result := notifydelivery.Result{}
	if env.NotificationDelivery != nil {
		ctx := env.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		result = env.NotificationDelivery.Deliver(ctx, request)
	}
	out := struct {
		Event   notifydelivery.TestEvent     `json:"event"`
		Source  notify.SourceID              `json:"source"`
		Channel string                       `json:"channel"`
		Native  notifydelivery.ChannelResult `json:"native"`
		Sound   notifydelivery.ChannelResult `json:"sound"`
	}{Event: event, Source: request.Notification.Source, Channel: selectedChannelText(channel), Native: result.Native, Sound: result.Sound}
	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(out); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
	} else {
		_, _ = fmt.Fprintln(env.Stdout, "Native: "+formatChannelResult(result.Native))
		_, _ = fmt.Fprintln(env.Stdout, "Sound: "+formatChannelResult(result.Sound))
	}
	return notificationTestExitCode(channel, result)
}

func containsArg(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func selectedChannelText(channel string) string {
	if channel == "" {
		return "all"
	}
	return channel
}

func formatChannelResult(result notifydelivery.ChannelResult) string {
	switch {
	case result.Delivered && result.Error != "":
		provider := result.Provider
		if provider == "" {
			provider = "provider"
		}
		return "delivered (" + provider + "); coordination failed: " + result.Error
	case result.Error != "":
		return "failed: " + result.Error
	case result.Delivered:
		return "delivered (" + result.Provider + ")"
	case result.Reason != "":
		return strings.ReplaceAll(string(result.Reason), "_", " ")
	default:
		return "not attempted"
	}
}

func notificationTestExitCode(channel string, result notifydelivery.Result) int {
	channels := []notifydelivery.ChannelResult{result.Native, result.Sound}
	switch channel {
	case notifydelivery.ChannelNative:
		channels = channels[:1]
	case notifydelivery.ChannelSound:
		channels = channels[1:]
	}
	for _, result := range channels {
		if result.Error != "" {
			return 1
		}
	}
	for _, result := range channels {
		if !result.Delivered {
			return 3
		}
	}
	return 0
}

// broadcastNotificationConfigReload makes an out-of-process targeted save
// live in every running Sidecar instance. The file is the source of truth; the
// request deliberately carries no config payload.
func broadcastNotificationConfigReload(env Env) {
	instances, err := uirequest.ListInstances(env.StateDir)
	if err != nil || len(instances) == 0 {
		return
	}
	_, _ = uirequest.WriteRequest(env.StateDir, uirequest.Request{
		Action: uirequest.ActionConfigReload,
		Origin: originForRequest(notifyOrigin(env)),
	})
}

func runNotifyPost(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("notify").FindSubcommand("post"))

	jsonOutput := false
	body := ""
	source := string(notify.SourceAgent)
	expiry := ""
	var targetSpecs []string
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func(flag string) (string, bool) {
			if strings.HasPrefix(arg, flag+"=") {
				return strings.TrimPrefix(arg, flag+"="), true
			}
			if arg == flag {
				if i+1 >= len(args) {
					return "", false
				}
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--body" || strings.HasPrefix(arg, "--body="):
			v, ok := value("--body")
			if !ok {
				cliErrf(env.Stderr, "--body requires text\n\n%s", help)
				return 2
			}
			body = v
		case arg == "--source" || strings.HasPrefix(arg, "--source="):
			v, ok := value("--source")
			if !ok {
				cliErrf(env.Stderr, "--source requires a source id\n\n%s", help)
				return 2
			}
			source = v
		case arg == "--expiry" || strings.HasPrefix(arg, "--expiry="):
			v, ok := value("--expiry")
			if !ok {
				cliErrf(env.Stderr, "--expiry requires a duration\n\n%s", help)
				return 2
			}
			expiry = v
		case arg == "--target" || strings.HasPrefix(arg, "--target="):
			v, ok := value("--target")
			if !ok {
				cliErrf(env.Stderr, "--target requires kind:value[:line][@project]\n\n%s", help)
				return 2
			}
			targetSpecs = append(targetSpecs, v)
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		cliErrf(env.Stderr, "notify post requires exactly one title\n\n%s", help)
		return 2
	}
	if !notify.ValidSource(notify.SourceID(source)) {
		cliErrf(env.Stderr, "unknown source %q (one of: %s)\n\n%s", source, strings.Join(notify.SourceIDs(), ", "), help)
		return 2
	}

	// Targets are parsed before anything is posted: a malformed target is a
	// usage error the agent can fix, not a notification that quietly does less
	// than it says.
	targets, err := notify.ParseTargetSpecs(targetSpecs)
	if err != nil {
		cliErrf(env.Stderr, "%s\n\n%s", err, help)
		return 2
	}

	n := notify.Notification{
		ID:      notify.NewID(),
		Source:  notify.SourceID(source),
		Title:   strings.TrimSpace(positional[0]),
		Body:    body,
		Origin:  notifyOrigin(env),
		Sticky:  false,
		Targets: targets,
	}
	switch strings.TrimSpace(strings.ToLower(expiry)) {
	case "":
		// The source's default expiry, applied when the record is stored.
	case "0", "never", "sticky":
		n.Sticky = true
	default:
		d, err := time.ParseDuration(expiry)
		if err != nil || d < 0 {
			cliErrf(env.Stderr, "invalid expiry %q (a duration such as 10s, or \"never\")\n\n%s", expiry, help)
			return 2
		}
		exp := time.Now().UTC().Add(d)
		n.ExpiresAt = &exp
	}
	// The user's per-source expiries live in config, and this process is the
	// one completing the record — without this a `sidecar notify post` would
	// carry the built-in default while the TUI used the configured one.
	if cfg, err := config.Load(); err == nil {
		notify.ApplyConfig(cfg.Notifications)
	}
	n = notify.Normalize(n, time.Now())

	payload, err := json.Marshal(n)
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}

	delivered, outcome := notifyDeliver(env, uirequest.Request{
		Origin:  originForRequest(n.Origin),
		Action:  uirequest.ActionNotify,
		Target:  uirequest.Target{Kind: uirequest.TargetKindNotification},
		Payload: payload,
	})

	if !delivered {
		// No instance took it. The notification is not lost: it goes straight
		// into the log the next Sidecar start reads.
		store, err := notify.Open(env.StateDir)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		defer func() { _ = store.Close() }()
		result, err := store.Post(n)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if result.Created && env.NotificationDelivery != nil {
			ctx := env.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			env.NotificationDelivery.Deliver(ctx, notifydelivery.Request{Notification: result.Notification})
		}
	}

	if jsonOutput {
		return writeNotifyJSON(env, notifyResult{Action: "post", ID: n.ID, Delivered: delivered, Notification: &n})
	}
	if delivered {
		_, _ = fmt.Fprintf(env.Stdout, "Posted %s (%s).\n", n.ID, n.Source)
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "Stored %s (%s); %s\n", n.ID, n.Source, outcome.explain())
	}
	return 0
}

func runNotifyDismiss(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("notify").FindSubcommand("dismiss"))

	jsonOutput := false
	var positional []string
	for _, arg := range args {
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		cliErrf(env.Stderr, "notify dismiss requires exactly one notification id\n\n%s", help)
		return 2
	}
	id := positional[0]

	all, err := notify.ReadAll(notify.Path(env.StateDir))
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	var target notify.Notification
	found := false
	for _, n := range all {
		if n.ID == id {
			target, found = n, true
			break
		}
	}
	if !found {
		cliErrf(env.Stderr, "no notification with id %q\n", id)
		return 3
	}
	caller := notifyOrigin(env)
	if !notify.MayDismiss(target, caller) {
		cliErrf(env.Stderr, "notification %s was posted by another caller; a caller may only dismiss its own\n", id)
		return 4
	}

	delivered, _ := notifyDeliver(env, notifyDismissRequest(caller, id))

	if !delivered {
		store, err := notify.Open(env.StateDir)
		if err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		defer func() { _ = store.Close() }()
		if err := store.Dismiss(id); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		if env.NotificationDelivery != nil {
			ctx := env.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			if err := env.NotificationDelivery.Remove(ctx, target); err != nil {
				cliErrln(env.Stderr, err)
				return 1
			}
		}
	}

	if jsonOutput {
		return writeNotifyJSON(env, notifyResult{Action: "dismiss", ID: id, Delivered: delivered})
	}
	_, _ = fmt.Fprintf(env.Stdout, "Dismissed %s.\n", id)
	return 0
}

func runNotifyList(env Env, args []string) int {
	help := RenderHelp(RootCommand().FindSubcommand("notify").FindSubcommand("list"))

	jsonOutput := false
	includeDismissed := false
	unreadOnly := false
	for _, arg := range args {
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--all":
			includeDismissed = true
		case arg == "--unread":
			unreadOnly = true
		default:
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
			return 2
		}
	}

	// Read the log directly rather than asking an instance: listing must work
	// with no TUI running, and reading never needs one.
	all, err := notify.ReadAll(notify.Path(env.StateDir))
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	items := notify.Active(all)
	switch {
	case unreadOnly:
		items = notify.Unread(all)
	case includeDismissed:
		items = all
	}

	if jsonOutput {
		out := struct {
			Unread int                   `json:"unread"`
			Items  []notify.Notification `json:"items"`
		}{Unread: notify.UnreadCount(all), Items: items}
		if out.Items == nil {
			out.Items = []notify.Notification{}
		}
		if err := json.NewEncoder(env.Stdout).Encode(out); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}

	if len(items) == 0 {
		_, _ = fmt.Fprintln(env.Stdout, "No notifications.")
		return 0
	}
	for _, n := range items {
		mark := "●"
		if n.Read() {
			mark = " "
		}
		if n.Dismissed() {
			mark = "×"
		}
		line := fmt.Sprintf("%s %s  %-8s %s", mark, n.ID, n.Source, n.Title)
		if n.Body != "" {
			line += "  — " + strings.ReplaceAll(n.Body, "\n", " ")
		}
		_, _ = fmt.Fprintln(env.Stdout, line)
	}
	return 0
}

// notifyDismissRequest builds the dismissal the app answers. The request
// carries the *caller's* origin, never the target's: sending the target's made
// the host compare the record against itself, so MayDismiss passed
// unconditionally and anything able to write a request file could dismiss
// anyone's notification. The id travels in Target.Value, which is what the host
// resolves the record from.
func notifyDismissRequest(caller notify.Origin, id string) uirequest.Request {
	return uirequest.Request{
		Origin: originForRequest(caller),
		Action: uirequest.ActionNotify,
		Target: uirequest.Target{Kind: uirequest.TargetKindNotification, Value: id},
	}
}

// notifyResult is the --json shape for post and dismiss.
type notifyResult struct {
	Action string `json:"action"`
	ID     string `json:"id"`
	// Delivered reports whether a running instance took it. False means the
	// log was written directly and it appears at the next start.
	Delivered    bool                 `json:"delivered"`
	Notification *notify.Notification `json:"notification,omitempty"`
}

func writeNotifyJSON(env Env, res notifyResult) int {
	if err := json.NewEncoder(env.Stdout).Encode(res); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

// deliveryOutcome is why a request was not taken. The CLI reported every
// failure as "no running Sidecar instance", which was a lie in the two cases
// that actually happen — an instance running on another project, and an
// instance that never answered — and sent the user looking for a process that
// was in front of them.
type deliveryOutcome int

const (
	deliveryNoInstance deliveryOutcome = iota
	deliveryDeclined
	deliveryTimedOut
	deliveryWriteFailed
	deliveryTaken
)

func (o deliveryOutcome) explain() string {
	switch o {
	case deliveryDeclined:
		return "no running Sidecar instance is showing this project, so it appears there within a second if one opens it, or at next start."
	case deliveryTimedOut:
		return "a running Sidecar instance did not answer in time, so it appears there within a second, or at next start."
	case deliveryWriteFailed:
		return "the request could not be handed to a running Sidecar instance, so it appears at next start."
	default:
		return "no running Sidecar instance, so it appears at next start."
	}
}

// notifyDeliver writes the request and reports whether a live instance took
// it, and why not when it did not. No announced instance means no request is
// written at all: there is nobody to answer, and the caller writes the log
// itself.
func notifyDeliver(env Env, req uirequest.Request) (bool, deliveryOutcome) {
	instances, err := uirequest.ListInstances(env.StateDir)
	if err != nil || len(instances) == 0 {
		return false, deliveryNoInstance
	}
	req.ID = uirequest.NewRequestID()
	req.Version = 1
	req.CreatedAt = time.Now().UTC()
	req.TTLMs = int(uirequest.DefaultTTL / time.Millisecond)
	if _, err := uirequest.WriteRequest(env.StateDir, req); err != nil {
		return false, deliveryWriteFailed
	}
	defer func() { _ = uirequest.Cleanup(env.StateDir, req.ID, req.Action) }()

	deadline := time.Now().Add(notifyWait)
	for time.Now().Before(deadline) {
		acks, err := uirequest.ReadAcks(env.StateDir, req.ID, req.Action)
		if err == nil {
			for _, ack := range acks {
				if ack.Status == uirequest.StatusOpened {
					return true, deliveryTaken
				}
			}
			if len(acks) >= len(instances) {
				// Every instance answered and none took it.
				return false, deliveryDeclined
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	return false, deliveryTimedOut
}

// notifyOrigin identifies the caller. Inside a Sidecar shell that is the
// shell's registered origin; anywhere else it is the working directory, which
// is enough to let a caller dismiss what it posted from the same place.
func notifyOrigin(env Env) notify.Origin {
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if dest, err := resolveOpenDestination(ctx, env.StateDir, "", ""); err == nil {
		return notify.Origin{
			TmuxSession: dest.Origin.TmuxSession,
			ProjectKey:  dest.Origin.ProjectKey,
			WorkDir:     dest.Origin.WorkDir,
			PID:         os.Getpid(),
		}
	}
	wd, _ := os.Getwd()
	return notify.Origin{WorkDir: wd, PID: os.Getpid()}
}

func originForRequest(o notify.Origin) uirequest.Origin {
	return uirequest.Origin{
		TmuxSession: o.TmuxSession,
		ProjectKey:  o.ProjectKey,
		WorkDir:     o.WorkDir,
		PID:         os.Getpid(),
	}
}
