package repoquality

import (
	"slices"
	"strings"
	"testing"
)

func TestCommitPolicyMatchesPinnedCommitlintContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		message     string
		wantOK      bool
		wantWarning string
	}{
		{name: "lower case", message: "feat(bfd): add peer", wantOK: true},
		{name: "sentence case", message: "docs: Update operator guide", wantOK: true},
		{name: "merge ignored", message: "Merge pull request #7 from example/topic", wantOK: true},
		{name: "revert ignored", message: "Revert accidental change", wantOK: true},
		{name: "unknown type", message: "feature(bfd): add peer"},
		{name: "scope-only release token", message: "release: publish version"},
		{name: "unknown scope", message: "feat(unknown): add peer"},
		{name: "upper case subject", message: "fix(bfd): REPAIR EVERYTHING"},
		{name: "header too long", message: "fix(bfd): " + strings.Repeat("a", 91)},
		{
			name:        "body line warning",
			message:     "fix(bfd): repair peer\n\n" + strings.Repeat("b", 121),
			wantOK:      true,
			wantWarning: "body-max-line-length",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			diagnostics := CheckCommit(test.message)
			if gotOK := !HasErrors(diagnostics); gotOK != test.wantOK {
				t.Fatalf("CheckCommit(%q) diagnostics = %#v, want ok=%t", test.message, diagnostics, test.wantOK)
			}
			if test.wantWarning != "" && !slices.ContainsFunc(diagnostics, func(diagnostic Diagnostic) bool {
				return diagnostic.Rule == test.wantWarning && diagnostic.level == warningSeverity
			}) {
				t.Fatalf("CheckCommit(%q) diagnostics = %#v, want warning %s", test.message, diagnostics, test.wantWarning)
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
		"MD047", "MD048", "MD050", "MD052", "MD053", "MD054", "MD055", "MD056", "MD059",
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
		{name: "mixed unordered marker", markdown: "- one\n* two\n", wantRule: "MD004"},
		{name: "inconsistent list indentation", markdown: "- one\n   - child\n  - child\n", wantRule: "MD005"},
		{name: "four space nested list", markdown: "- parent\n    - child\n", wantRule: "MD007"},
		{name: "trailing spaces", markdown: "# Heading   \n", wantRule: "MD009"},
		{name: "hard tab", markdown: "# Heading\n\n\ttext\n", wantRule: "MD010"},
		{name: "reversed link", markdown: "# Heading\n\n(text)[https://example.com]\n", wantRule: "MD011"},
		{name: "multiple blanks", markdown: "# Heading\n\n\nParagraph\n", wantRule: "MD012"},
		{name: "command without output", markdown: "# Heading\n\n```console\n$ go test ./...\n```\n", wantRule: "MD014"},
		{name: "missing ATX space", markdown: "#Heading\n", wantRule: "MD018"},
		{name: "closed ATX missing inner space", markdown: "# Heading#\n", wantRule: "MD020"},
		{name: "indented heading", markdown: "  # Heading\n", wantRule: "MD023"},
		{name: "duplicate sibling heading", markdown: "## Duplicate\n\n## Duplicate\n", wantRule: "MD024"},
		{name: "heading punctuation", markdown: "# Heading.\n", wantRule: "MD026"},
		{name: "blockquote spacing", markdown: ">  quote\n", wantRule: "MD027"},
		{name: "blank blockquote line", markdown: "> first\n\n> second\n", wantRule: "MD028"},
		{name: "ordered prefix", markdown: "1. first\n3. third\n", wantRule: "MD029"},
		{name: "list marker spacing", markdown: "-  item\n", wantRule: "MD030"},
		{name: "mixed horizontal rule", markdown: "---\n\n***\n", wantRule: "MD035"},
		{name: "emphasis inner space", markdown: "# Heading\n\n** bold **\n", wantRule: "MD037"},
		{name: "code inner space", markdown: "# Heading\n\n` code`\n", wantRule: "MD038"},
		{name: "link inner space", markdown: "# Heading\n\n[ text ](https://example.com)\n", wantRule: "MD039"},
		{name: "empty link", markdown: "# Heading\n\n[empty]()\n", wantRule: "MD042"},
		{name: "missing image alt", markdown: "# Heading\n\n![](image.png)\n", wantRule: "MD045"},
		{name: "indented code", markdown: "Paragraph\n\n    code\n", wantRule: "MD046"},
		{name: "missing final newline", markdown: "# Heading", wantRule: "MD047"},
		{name: "mixed fence style", markdown: "```text\none\n```\n\n~~~text\ntwo\n~~~\n", wantRule: "MD048"},
		{name: "underscore strong", markdown: "# Heading\n\n__strong__\n", wantRule: "MD050"},
		{name: "undefined reference", markdown: "# Heading\n\n[text][missing]\n", wantRule: "MD052"},
		{name: "unused reference", markdown: "# Heading\n\n[unused]: https://example.com\n", wantRule: "MD053"},
		{name: "mixed table pipes", markdown: "| a | b |\n| --- | ---\n  1 | 2 |\n", wantRule: "MD055"},
		{name: "table column count", markdown: "| a | b |\n| --- | --- |\n| 1 |\n", wantRule: "MD056"},
		{name: "nondescriptive link", markdown: "# Heading\n\n[click here](https://example.com)\n", wantRule: "MD059"},
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
