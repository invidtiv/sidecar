package styles

import (
	"bytes"
	"testing"

	"github.com/alecthomas/chroma/v2/quick"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

func TestSidecarModernSyntaxThemeRegistered(t *testing.T) {
	st := chromastyles.Get(SidecarModernSyntaxThemeName)
	if st == nil {
		t.Fatalf("Chroma style %q is not registered", SidecarModernSyntaxThemeName)
	}

	if SidecarModernTheme.Colors.SyntaxTheme != SidecarModernSyntaxThemeName {
		t.Errorf("SidecarModernTheme.Colors.SyntaxTheme = %q, want %q",
			SidecarModernTheme.Colors.SyntaxTheme, SidecarModernSyntaxThemeName)
	}
}

func TestSidecarModernSyntaxHighlighting(t *testing.T) {
	snippets := []struct {
		lang string
		ext  string
		code string
	}{
		{
			lang: "Go",
			ext:  ".go",
			code: "package main\n\nimport \"fmt\"\n\ntype Model struct { Name string }\n\nfunc main() {\n\tif true {\n\t\tfmt.Println(\"hello\")\n\t}\n}\n",
		},
		{
			lang: "TypeScript",
			ext:  ".ts",
			code: "import React from 'react';\n\ninterface Props { count: number; }\nexport const App = ({ count }: Props) => <div>{count}</div>;\n",
		},
		{
			lang: "Python",
			ext:  ".py",
			code: "import os\n\ndef run(items: list[str]) -> int:\n    # comment\n    return len(items)\n",
		},
		{
			lang: "JSON",
			ext:  ".json",
			code: "{\n  \"name\": \"sidecar\",\n  \"version\": 1,\n  \"enabled\": true\n}\n",
		},
		{
			lang: "Markdown",
			ext:  ".md",
			code: "# Header\n\nSome text with `code` and [link](https://example.com).\n",
		},
	}

	for _, s := range snippets {
		t.Run(s.lang, func(t *testing.T) {
			buf := new(bytes.Buffer)
			if err := quick.Highlight(buf, s.code, s.ext, "terminal256", SidecarModernSyntaxThemeName); err != nil {
				t.Fatalf("quick.Highlight failed for %s: %v", s.lang, err)
			}
			if buf.Len() == 0 {
				t.Fatalf("quick.Highlight produced empty output for %s", s.lang)
			}
		})
	}
}

func TestSidecarModernSyntaxContrastOnCanvas(t *testing.T) {
	st := chromastyles.Get(SidecarModernSyntaxThemeName)
	if st == nil {
		t.Fatalf("Chroma style %q is not registered", SidecarModernSyntaxThemeName)
	}

	canvasBg := SidecarModernTheme.Colors.BgPrimary

	for _, tt := range st.Types() {
		entry := st.Get(tt)
		if !entry.Colour.IsSet() {
			continue
		}
		hex := entry.Colour.String()
		ratio := ContrastRatio(hex, canvasBg)

		// Comments and subtle whitespace are allowed lower contrast, but real code must meet AA (>= 4.5:1)
		minRatio := 4.5
		if tt.String() == "Comment" || tt.String() == "CommentSingle" || tt.String() == "CommentMultiline" || tt.String() == "CommentHashbang" || tt.String() == "CommentDoc" || tt.String() == "TextWhitespace" {
			minRatio = 3.0
		}

		if ratio < minRatio-0.01 {
			t.Errorf("token %v color %s has contrast ratio %.2f < %.2f on canvas %s",
				tt, hex, ratio, minRatio, canvasBg)
		}
	}
}
