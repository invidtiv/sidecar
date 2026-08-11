package version

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ProductStatusMsg reports the discovered state of one product. It is the
// single product-neutral result of a release check; the app stores one per
// product instead of parallel per-product fields.
type ProductStatusMsg struct {
	Target       Target
	ReleaseNotes string
	ReleaseURL   string
}

// CheckProductAsync returns a Bubble Tea command that discovers one product's
// installed version, release status, and install provenance in the background.
//
// currentVersion is the in-process version for Sidecar; pass "" for products
// whose version must be read from the installed executable. Callers only
// schedule this for products that are effectively enabled, so a disabled
// feature adds no startup process or network work.
func CheckProductAsync(d Descriptor, currentVersion string, force bool) tea.Cmd {
	return func() tea.Msg {
		return checkProduct(context.Background(), DefaultEnvironment(), d, currentVersion, force)
	}
}

func checkProduct(ctx context.Context, env *Environment, d Descriptor, currentVersion string, force bool) ProductStatusMsg {
	target := Target{
		Product:     d.Product,
		DisplayName: d.DisplayName,
		Enabled:     true,
	}

	current := currentVersion
	if d.Product == ProductSidecar {
		target.Installed = true
	} else {
		if _, err := env.LookPath(d.Executable); err != nil {
			return ProductStatusMsg{Target: target}
		}
		target.Installed = true
		current = InstalledVersion(ctx, env, d)
	}
	target.CurrentVersion = current

	// A development build has no release to compare against; leave it as
	// "no update", never as a failed check.
	if isDevelopmentVersion(current) || NormalizeVersion(current) == "" {
		target.Install = DetectInstallation(ctx, env, d, "")
		if target.CurrentVersion == "" {
			target.CurrentVersion = "development build"
		}
		return ProductStatusMsg{Target: target}
	}

	msg := ProductStatusMsg{}
	if !force {
		if cached, err := LoadCacheFile(d.CacheFile); err == nil && IsCacheValid(cached, current) {
			target.LatestVersion = cached.LatestVersion
			target.HasUpdate = cached.HasUpdate
			target.Install = DetectInstallation(ctx, env, d, cached.LatestVersion)
			msg.Target = target
			return msg
		}
	}

	result := CheckRepo(d.RepoOwner, d.RepoName, current)
	if result.Error != nil {
		target.CheckFailed = true
		target.Install = DetectInstallation(ctx, env, d, "")
		msg.Target = target
		return msg
	}

	_ = SaveCacheFile(d.CacheFile, &CacheEntry{
		LatestVersion:  result.LatestVersion,
		CurrentVersion: current,
		CheckedAt:      time.Now(),
		HasUpdate:      result.HasUpdate,
	})

	target.LatestVersion = result.LatestVersion
	target.HasUpdate = result.HasUpdate
	target.Install = DetectInstallation(ctx, env, d, result.LatestVersion)

	msg.Target = target
	msg.ReleaseNotes = result.ReleaseNotes
	msg.ReleaseURL = result.UpdateURL
	return msg
}
