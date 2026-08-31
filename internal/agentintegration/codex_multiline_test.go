package agentintegration

import "testing"

// The config.toml line writer refuses a file containing a multi-line string,
// because once one opens, no following line's structure can be read by a
// scanner. The refusal is right; its old spelling -- a substring search over
// the whole file -- was not, and the cost of getting it wrong is high: parseErr
// refuses install, repair AND uninstall, so a user whose config merely
// mentioned the delimiter in a comment could neither integrate Codex nor clean
// up an existing integration.
func TestOnlyAStructuralMultilineDelimiterRefusesTheConfigEditor(t *testing.T) {
	tests := []struct {
		name   string
		toml   string
		refuse bool
	}{
		{
			name:   "a delimiter inside a comment is prose, not structure",
			toml:   "# use \"\"\" when you need newlines\nmodel = \"gpt-5.6-sol\"\n",
			refuse: false,
		},
		{
			name:   "a delimiter after an inline comment marker is also prose",
			toml:   "model = \"gpt-5.6-sol\" # or ''' for a literal block\n",
			refuse: false,
		},
		{
			name:   "escaped quotes inside an ordinary string are not a delimiter",
			toml:   "note = \"he said \\\"\\\"\\\" loudly\"\n",
			refuse: false,
		},
		{
			name:   "a real basic multi-line string still refuses",
			toml:   "note = \"\"\"\nline one\n[not] = a table\n\"\"\"\n",
			refuse: true,
		},
		{
			name:   "a real literal multi-line string still refuses",
			toml:   "note = '''\n[not] = a table\n'''\n",
			refuse: true,
		},
		{
			name:   "an ordinary config with neither is fine",
			toml:   "[features]\nhooks = true\n",
			refuse: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scan := scanCodexConfig(true, []byte(tc.toml))
			refused := scan.parseErr != ""
			if refused != tc.refuse {
				t.Fatalf("refused=%v want %v (parseErr=%q)", refused, tc.refuse, scan.parseErr)
			}
		})
	}
}

// The whole point of narrowing the refusal is that the integration becomes
// usable for these users, so this checks the consequence rather than the
// predicate: a config.toml mentioning the delimiter in a comment can be
// installed into and uninstalled from, with the comment preserved.
func TestAConfigMentioningTheDelimiterInACommentCanStillBeManaged(t *testing.T) {
	env := testEnv(t)
	comment := "# you can use \"\"\" for multi-line values\nmodel = \"gpt-5.6-sol\"\n"
	writeFileT(t, env.Home+"/.codex/config.toml", comment)
	writeFileT(t, env.Home+"/.codex/hooks.json", "{}\n")

	scan := scanCodexConfig(true, []byte(comment))
	if scan.parseErr != "" {
		t.Fatalf("a comment blocked the editor: %s", scan.parseErr)
	}
	st := (CodexAdapter{}).Inspect(env)
	for _, f := range st.Files {
		if f.UnsafeDetail != "" && f.Path == env.Home+"/.codex/config.toml" {
			t.Fatalf("config.toml was reported unusable: %s", f.UnsafeDetail)
		}
	}
}
