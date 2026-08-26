package repoquality

import (
	"slices"
	"strings"
	"testing"
)

func TestCommitPolicyMatchesPinnedCommitlintContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		wantOK  bool
	}{
		{name: "lower case", message: "feat(bfd): add peer", wantOK: true},
		{name: "sentence case", message: "docs: Update operator guide", wantOK: true},
		{name: "merge ignored", message: "Merge pull request #7 from example/topic", wantOK: true},
		{name: "revert ignored", message: "Revert accidental change", wantOK: true},
		{name: "unknown type", message: "feature(bfd): add peer"},
		{name: "unknown scope", message: "feat(unknown): add peer"},
		{name: "upper case subject", message: "fix(bfd): REPAIR EVERYTHING"},
		{name: "header too long", message: "fix(bfd): " + strings.Repeat("a", 91)},
		{name: "body line too long", message: "fix(bfd): repair peer\n\n" + strings.Repeat("b", 121)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			diagnostics := CheckCommit(test.message)
			if gotOK := len(diagnostics) == 0; gotOK != test.wantOK {
				t.Fatalf("CheckCommit(%q) diagnostics = %#v, want ok=%t", test.message, diagnostics, test.wantOK)
			}
		})
	}
}

func TestMarkdownPolicyMatchesPinnedMarkdownlintContract(t *testing.T) {
	t.Parallel()

	wantRules := []string{
		"MD003", "MD004", "MD005", "MD007", "MD009", "MD010", "MD011", "MD012", "MD014",
		"MD018", "MD020", "MD023", "MD024", "MD026", "MD027", "MD028", "MD029", "MD030",
		"MD035", "MD037", "MD038", "MD039", "MD042", "MD043", "MD044", "MD045", "MD046",
		"MD047", "MD048", "MD049", "MD050", "MD052", "MD053", "MD054", "MD055", "MD056", "MD059",
	}
	if got := MarkdownRules(); !slices.Equal(got, wantRules) {
		t.Fatalf("MarkdownRules() = %v, want %v", got, wantRules)
	}

	tests := []struct {
		name     string
		markdown string
		wantRule string
	}{
		{name: "valid configured style", markdown: "# Heading\n\n- parent\n  - child\n\n```text\ncode\n```\n"},
		{name: "setext heading", markdown: "Heading\n=======\n", wantRule: "MD003"},
		{name: "four space nested list", markdown: "- parent\n    - child\n", wantRule: "MD007"},
		{name: "duplicate sibling heading", markdown: "## Duplicate\n\n## Duplicate\n", wantRule: "MD024"},
		{name: "indented code", markdown: "Paragraph\n\n    code\n", wantRule: "MD046"},
		{name: "missing final newline", markdown: "# Heading", wantRule: "MD047"},
		{name: "underscore strong", markdown: "# Heading\n\n__strong__\n", wantRule: "MD050"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			diagnostics := CheckMarkdown(test.name+".md", []byte(test.markdown))
			if test.wantRule == "" {
				if len(diagnostics) != 0 {
					t.Fatalf("CheckMarkdown() diagnostics = %#v, want none", diagnostics)
				}
				return
			}
			if !hasRule(diagnostics, test.wantRule) {
				t.Fatalf("CheckMarkdown() diagnostics = %#v, want rule %s", diagnostics, test.wantRule)
			}
		})
	}
}

func hasRule(diagnostics []Diagnostic, rule string) bool {
	return slices.ContainsFunc(diagnostics, func(diagnostic Diagnostic) bool {
		return diagnostic.Rule == rule
	})
}
