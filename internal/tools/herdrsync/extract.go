package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// The extractors below read stable shapes out of Herdr's Rust and MDX sources.
// Every one of them fails loudly when the shape it expects is missing, because
// a silent empty table would look like "Herdr dropped that agent" on the next
// review. On failure the caller aborts before writing, so the previous JSON
// stands.

var (
	fnBodyRE = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?s)fn ` + regexp.QuoteMeta(name) + `\([^)]*\)[^{]*\{(.*?)\n\}`)
	}
	// `"claude" | "claude-code" => Some(Agent::Claude),`
	lookupArmRE = regexp.MustCompile(`(?m)^\s*((?:"[^"]+"\s*\|\s*)*"[^"]+")\s*=>\s*Some\(Agent::(\w+)\)\s*,`)
	// `Agent::Claude => "claude",`
	labelArmRE = regexp.MustCompile(`(?m)^\s*Agent::(\w+)\s*=>\s*"([^"]+)"\s*,`)
	// `.strip_prefix("muse-bin-")` inside is_<agent>_versioned_binary
	versionedPrefixRE = regexp.MustCompile(`fn is_(\w+?)_versioned_binary\([^)]*\)[^{]*\{(?s:.*?)strip_prefix\("([^"]+)"\)`)
	quotedRE          = regexp.MustCompile(`"([^"]*)"`)
)

// extractAliases reads lookup_agent, agent_label, is_generic_runtime_or_shell,
// and normalized_agent_lookup_name out of src/detect/mod.rs.
func extractAliases(src source, ref string) (*manifests.Aliases, error) {
	data, err := src.read(aliasSource)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", aliasSource, err)
	}
	content := string(data)

	labels, err := extractAgentLabels(content)
	if err != nil {
		return nil, err
	}

	lookupBody, err := functionBody(content, "lookup_agent")
	if err != nil {
		return nil, err
	}
	arms := lookupArmRE.FindAllStringSubmatch(lookupBody, -1)
	if len(arms) == 0 {
		return nil, fmt.Errorf("no `\"name\" => Some(Agent::X)` arms found in lookup_agent; the match shape changed")
	}
	agents := make(map[string][]string, len(arms))
	for _, arm := range arms {
		variant := arm[2]
		id, ok := labels[variant]
		if !ok {
			return nil, fmt.Errorf("lookup_agent names Agent::%s but agent_label has no label for it", variant)
		}
		var names []string
		for _, quoted := range quotedRE.FindAllStringSubmatch(arm[1], -1) {
			names = append(names, quoted[1])
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("lookup_agent arm for Agent::%s has no quoted names", variant)
		}
		agents[id] = append(agents[id], names...)
	}
	for id := range agents {
		agents[id] = dedupeSorted(agents[id])
	}

	runtimeBody, err := functionBody(content, "is_generic_runtime_or_shell")
	if err != nil {
		return nil, err
	}
	var runtimes []string
	for _, quoted := range quotedRE.FindAllStringSubmatch(runtimeBody, -1) {
		runtimes = append(runtimes, quoted[1])
	}
	if len(runtimes) == 0 {
		return nil, fmt.Errorf("is_generic_runtime_or_shell lists no quoted names; the match shape changed")
	}
	runtimes = dedupeSorted(runtimes)

	pythonBody, err := functionBody(content, "is_python_runtime")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(pythonBody, `strip_prefix("python")`) {
		return nil, fmt.Errorf("is_python_runtime no longer strips a \"python\" prefix; the shape changed")
	}

	prefixes := map[string]string{}
	for _, m := range versionedPrefixRE.FindAllStringSubmatch(content, -1) {
		id := m[1]
		if _, ok := agents[id]; !ok {
			return nil, fmt.Errorf("is_%s_versioned_binary names an agent lookup_agent does not: %s", id, id)
		}
		prefixes[id] = m[2]
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("no is_<agent>_versioned_binary helper found; the shape changed")
	}

	normalizeBody, err := functionBody(content, "normalized_agent_lookup_name")
	if err != nil {
		return nil, err
	}
	var suffixes []string
	for _, quoted := range quotedRE.FindAllStringSubmatch(normalizeBody, -1) {
		suffixes = append(suffixes, quoted[1])
	}
	if len(suffixes) == 0 {
		return nil, fmt.Errorf("normalized_agent_lookup_name strips no quoted suffixes; the shape changed")
	}

	return &manifests.Aliases{
		SchemaVersion:   1,
		GeneratedFrom:   aliasSource,
		HerdrRef:        ref,
		Agents:          agents,
		GenericRuntimes: runtimes,
		PythonRuntimeRule: "python, or python<segment>[.<segment>...] where every dot-separated segment " +
			"after the prefix is a non-empty run of ASCII digits (is_python_runtime)",
		VersionedBinaryPrefixes: prefixes,
		NormalizedSuffixes:      suffixes,
	}, nil
}

func extractAgentLabels(content string) (map[string]string, error) {
	body, err := functionBody(content, "agent_label")
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for _, arm := range labelArmRE.FindAllStringSubmatch(body, -1) {
		labels[arm[1]] = arm[2]
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("no `Agent::X => \"label\"` arms found in agent_label; the match shape changed")
	}
	return labels, nil
}

func functionBody(content, name string) (string, error) {
	match := fnBodyRE(name).FindStringSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("could not find fn %s; the source shape changed", name)
	}
	return match[1], nil
}

func dedupeSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// --- authority -------------------------------------------------------------------

// authorityDisplayNames maps the display names in Herdr's published agent table
// to the agent ids the rest of the system uses. Herdr's docs are prose and carry
// no ids, so this is the one hand-maintained bridge in the sync; an unmapped row
// fails the extraction rather than being dropped.
var authorityDisplayNames = map[string]string{
	"Pi":                 "pi",
	"OMP":                "omp",
	"GitHub Copilot CLI": "copilot",
	"Devin CLI":          "devin",
	"Kimi Code CLI":      "kimi",
	"Hermes Agent":       "hermes",
	"Qoder CLI":          "qodercli",
	"Qwen Code":          "qwen",
	"Droid":              "droid",
	"OpenCode":           "opencode",
	"Kilo Code CLI":      "kilo",
	"MastraCode":         "mastracode",
	"Claude Code":        "claude",
	"Codex":              "codex",
	"Cursor Agent CLI":   "cursor",
	"Amp":                "amp",
	"Grok CLI":           "grok",
	"Antigravity CLI":    "agy",
	"Kiro CLI":           "kiro",
	"Maki":               "maki",
	"Muse":               "muse",
	"Gemini CLI":         "gemini",
	"Cline":              "cline",
}

// assetDirAgent maps an integration asset directory to its agent id where the
// two spellings differ.
var assetDirAgent = map[string]string{"antigravity_cli": "agy"}

var (
	authorityRowRE = regexp.MustCompile(`(?m)^\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*$`)
	// "Detected but less thoroughly tested: Gemini CLI and Cline."
	lessTestedRE       = regexp.MustCompile(`Detected but less thoroughly tested:\s*([^.]+)\.`)
	integrationVersion = regexp.MustCompile(`HERDR_INTEGRATION_VERSION=(\d+)`)
)

// extractAuthority reads the per-agent authority table out of Herdr's published
// agents.mdx and pairs it with the HERDR_INTEGRATION_VERSION each provider's
// integration assets carry.
//
// The assets are read by the caller and passed in, because the same tree is
// vendored in the same run and reading it twice would be 34 more `git show`
// invocations to learn what is already in hand.
func extractAuthority(src source, ref string, assets []integrationAssetDir) (*manifests.Authority, error) {
	data, err := src.read(authoritySource)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", authoritySource, err)
	}
	content := string(data)

	agents, err := parseAuthorityTable(content)
	if err != nil {
		return nil, err
	}

	for _, dir := range assets {
		if dir.version <= 0 {
			continue
		}
		entry, ok := agents[dir.id]
		if !ok {
			// An integration for an agent the table does not list is a real
			// change in Herdr; say so rather than silently discarding it.
			return nil, fmt.Errorf("integration assets exist for %q but the agent table does not list it", dir.id)
		}
		entry.IntegrationVersion = dir.version
		entry.IntegrationAssetDir = dir.dir
		agents[dir.id] = entry
	}

	return &manifests.Authority{
		SchemaVersion: 1,
		GeneratedFrom: []string{authoritySource, assetsDir},
		HerdrRef:      ref,
		Agents:        agents,
	}, nil
}

func parseAuthorityTable(content string) (map[string]manifests.AuthorityAgent, error) {
	agents := map[string]manifests.AuthorityAgent{}
	for _, row := range authorityRowRE.FindAllStringSubmatch(content, -1) {
		name := strings.TrimSpace(row[1])
		stateAuthority := strings.TrimSpace(row[2])
		role := strings.TrimSpace(row[3])
		if name == "Agent" || strings.HasPrefix(name, "---") {
			continue
		}
		id, ok := authorityDisplayNames[name]
		if !ok {
			return nil, fmt.Errorf("agents.mdx lists %q, which has no agent id mapping; add it to authorityDisplayNames", name)
		}
		agents[id] = manifests.AuthorityAgent{
			DisplayName:        name,
			LifecycleAuthority: lifecycleAuthority(stateAuthority, role),
			StateAuthority:     stateAuthority,
			IntegrationRole:    role,
		}
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("no rows found in the agents.mdx authority table; the table shape changed")
	}

	// The prose line after the table names the agents Herdr detects but does
	// not list as a table row. They have a screen manifest and no integration.
	match := lessTestedRE.FindStringSubmatch(content)
	if match == nil {
		return nil, fmt.Errorf("could not find the \"Detected but less thoroughly tested\" line; the doc shape changed")
	}
	for _, name := range splitAndList(match[1]) {
		id, ok := authorityDisplayNames[name]
		if !ok {
			return nil, fmt.Errorf("agents.mdx names %q as less thoroughly tested, which has no agent id mapping", name)
		}
		if _, exists := agents[id]; exists {
			continue
		}
		agents[id] = manifests.AuthorityAgent{
			DisplayName:        name,
			LifecycleAuthority: manifests.AuthorityNone,
			StateAuthority:     "screen manifest",
			IntegrationRole:    "none",
		}
	}
	return agents, nil
}

// lifecycleAuthority derives the three-valued authority from the two prose
// columns: hooks or a plugin named in the state column means Herdr trusts the
// integration for state, an integration role of "session" means it trusts it
// only for identity, and anything else means there is no integration at all.
func lifecycleAuthority(stateAuthority, role string) string {
	lower := strings.ToLower(stateAuthority)
	if strings.Contains(lower, "lifecycle hooks") || strings.Contains(lower, "lifecycle plugin") {
		return manifests.AuthorityHooks
	}
	switch strings.ToLower(role) {
	case "session", "state and session":
		return manifests.AuthoritySessionIdentity
	default:
		return manifests.AuthorityNone
	}
}

func splitAndList(text string) []string {
	text = strings.ReplaceAll(text, " and ", ",")
	var out []string
	for _, part := range strings.Split(text, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// integrationAsset is one file under Herdr's src/integration/assets.
type integrationAsset struct {
	// path is the upstream path, e.g. src/integration/assets/claude/herdr-agent-state.sh.
	path string
	// name is the base name, which is also the name the file is vendored under.
	name string
	data []byte
	// version is the HERDR_INTEGRATION_VERSION this file declares, or 0 when it
	// declares none. Herdr's shared test files declare none.
	version int
}

// integrationAssetDir is one provider directory and the version its files agree
// on.
type integrationAssetDir struct {
	// dir is the directory name under src/integration/assets.
	dir string
	// id is the Herdr agent id, which differs from dir for antigravity_cli.
	id string
	// version is 0 when no file in the directory declares one.
	version int
	files   []integrationAsset
}

// integrationAssets reads Herdr's whole integration-asset tree once and returns
// the per-provider directories plus the files that sit directly under the
// assets root, which today is the shared herdr-agent-state.test.ts.
//
// It is the one place HERDR_INTEGRATION_VERSION is read: the authority table
// pairs each agent with its version and the vendoring locks each file with the
// version it declares, and both take those numbers from here. Files in the same
// directory must agree; a disagreement means Herdr shipped a half-bumped
// integration and is worth failing on rather than picking a side.
func integrationAssets(src source) ([]integrationAssetDir, []integrationAsset, error) {
	read := func(rel string) (integrationAsset, error) {
		data, err := src.read(rel)
		if err != nil {
			return integrationAsset{}, fmt.Errorf("read %s: %w", rel, err)
		}
		asset := integrationAsset{path: rel, name: path.Base(rel), data: data}
		if match := integrationVersion.FindStringSubmatch(string(data)); match != nil {
			value, err := strconv.Atoi(match[1])
			if err != nil {
				return integrationAsset{}, fmt.Errorf("%s has a non-numeric HERDR_INTEGRATION_VERSION %q", rel, match[1])
			}
			asset.version = value
		}
		return asset, nil
	}

	subdirs, err := src.listDirs(assetsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("list %s: %w", assetsDir, err)
	}
	var dirs []integrationAssetDir
	versioned := 0
	for _, dir := range subdirs {
		providerDir := path.Join(assetsDir, dir)
		names, err := src.list(providerDir)
		if err != nil {
			return nil, nil, fmt.Errorf("list %s: %w", providerDir, err)
		}
		// A provider directory is flat upstream and the vendoring assumes it:
		// list returns blobs only, so a subdirectory would be dropped without a
		// word, the provider would lock fewer files than it has -- possibly none
		// -- and the report would show a version rollback for a provider that
		// only moved a file down a level. Refusing is the honest answer: it
		// costs one sync run and makes the next one a deliberate change to the
		// vendoring, where recursing silently would quietly unpin a ported
		// provider's asset and prune the copy the port was written against.
		nested, err := src.listDirs(providerDir)
		if err != nil {
			return nil, nil, fmt.Errorf("list subdirectories of %s: %w", providerDir, err)
		}
		if len(nested) > 0 {
			return nil, nil, fmt.Errorf("%s has subdirectories (%s); the vendoring reads one flat "+
				"directory per provider, so a nested asset tree needs herdrsync taught about it "+
				"rather than vendored half",
				providerDir, strings.Join(nested, ", "))
		}
		entry := integrationAssetDir{dir: dir, id: dir}
		if mapped, ok := assetDirAgent[dir]; ok {
			entry.id = mapped
		}
		for _, name := range names {
			asset, err := read(path.Join(assetsDir, dir, name))
			if err != nil {
				return nil, nil, err
			}
			if asset.version > 0 {
				if entry.version > 0 && entry.version != asset.version {
					return nil, nil, fmt.Errorf("%s carries HERDR_INTEGRATION_VERSION %d and %d in the same directory",
						dir, entry.version, asset.version)
				}
				entry.version = asset.version
			}
			entry.files = append(entry.files, asset)
		}
		if len(entry.files) == 0 {
			return nil, nil, fmt.Errorf("%s holds no files; a provider that vendors nothing rolls its "+
				"version back to 0, drops out of the authority table and has its vendored copy pruned, "+
				"which is a sync to refuse rather than to publish", providerDir)
		}
		if entry.version > 0 {
			versioned++
		}
		dirs = append(dirs, entry)
	}
	if versioned == 0 {
		return nil, nil, fmt.Errorf("no HERDR_INTEGRATION_VERSION found under %s; the asset shape changed", assetsDir)
	}

	rootNames, err := src.list(assetsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("list %s: %w", assetsDir, err)
	}
	var shared []integrationAsset
	for _, name := range rootNames {
		asset, err := read(path.Join(assetsDir, name))
		if err != nil {
			return nil, nil, err
		}
		shared = append(shared, asset)
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].id < dirs[j].id })
	return dirs, shared, nil
}
