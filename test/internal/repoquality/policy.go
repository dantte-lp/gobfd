// Package repoquality implements repository-owned commit and documentation
// quality policies without a Node.js runtime.
package repoquality

import (
	"regexp"
	"strings"
)

// Diagnostic identifies one repository policy violation.
type Diagnostic struct {
	Rule    string
	Line    int
	Message string
}

var (
	commitHeader = regexp.MustCompile(`^([a-z]+)(?:\(([a-z0-9-]+)\))?!?: (.+)$`)
	atxHeading   = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	listItem     = regexp.MustCompile(`^( +)[-*+] `)
	setextMarker = regexp.MustCompile(`^(?:=+|-+)[ \t]*$`)
)

const (
	headerMaxBytes = 100
	bodyMaxBytes   = 120

	allowedCommitTypes  = ",build,chore,ci,docs,feat,fix,perf,refactor,release,revert,style,test,"
	allowedCommitScopes = ",api,bfd,build,ci,cli,config,deps,docs,examples,gobgp,interop,lint," +
		"metrics,netio,release,sdnotify,security,server,test,"
	markdownRuleList = "MD003,MD004,MD005,MD007,MD009,MD010,MD011,MD012,MD014," +
		"MD018,MD020,MD023,MD024,MD026,MD027,MD028,MD029,MD030," +
		"MD035,MD037,MD038,MD039,MD042,MD043,MD044,MD045,MD046," +
		"MD047,MD048,MD049,MD050,MD052,MD053,MD054,MD055,MD056,MD059"
)

// MarkdownRules returns the exact markdownlint 0.41 rule set retained by the
// repository configuration.
func MarkdownRules() []string {
	return strings.Split(markdownRuleList, ",")
}

// CheckCommit checks one message against the committed Conventional Commit
// type, scope, subject-case, and length contract.
func CheckCommit(message string) []Diagnostic {
	if ignoredCommit(message) {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	header := lines[0]
	diagnostics := make([]Diagnostic, 0)
	if len(header) > headerMaxBytes {
		diagnostics = append(diagnostics, newDiagnostic("header-max-length", 1, "header exceeds 100 bytes"))
	}
	diagnostics = append(diagnostics, checkCommitHeader(header)...)
	for index, line := range lines[1:] {
		if len(line) > bodyMaxBytes {
			diagnostics = append(
				diagnostics,
				newDiagnostic("body-max-line-length", index+2, "body line exceeds 120 bytes"),
			)
		}
	}
	return diagnostics
}

func checkCommitHeader(header string) []Diagnostic {
	match := commitHeader.FindStringSubmatch(header)
	if match == nil {
		return []Diagnostic{newDiagnostic("header-format", 1, "header is not Conventional Commit syntax")}
	}
	diagnostics := make([]Diagnostic, 0, 3)
	if !containsToken(allowedCommitTypes, match[1]) {
		diagnostics = append(diagnostics, newDiagnostic("type-enum", 1, "commit type is not allowed"))
	}
	if match[2] != "" && !containsToken(allowedCommitScopes, match[2]) {
		diagnostics = append(diagnostics, newDiagnostic("scope-enum", 1, "commit scope is not allowed"))
	}
	if subjectIsUpper(match[3]) {
		diagnostics = append(
			diagnostics,
			newDiagnostic("subject-case", 1, "subject is neither lower nor sentence case"),
		)
	}
	return diagnostics
}

func containsToken(list, token string) bool {
	return strings.Contains(list, ","+token+",")
}

func newDiagnostic(rule string, line int, message string) Diagnostic {
	return Diagnostic{Rule: rule, Line: line, Message: message}
}

func ignoredCommit(message string) bool {
	first, _, _ := strings.Cut(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	return strings.HasPrefix(first, "Merge pull request") ||
		strings.HasPrefix(first, "Merge branch") ||
		strings.HasPrefix(first, "Merge tag") ||
		strings.HasPrefix(first, "Revert ") ||
		strings.HasPrefix(first, "revert ") ||
		strings.HasPrefix(first, "Reapply ") ||
		strings.HasPrefix(first, "reapply ") ||
		strings.HasPrefix(first, "amend!") ||
		strings.HasPrefix(first, "fixup!") ||
		strings.HasPrefix(first, "squash!")
}

func subjectIsUpper(subject string) bool {
	hasLetter := false
	for _, character := range subject {
		if character >= 'a' && character <= 'z' {
			return false
		}
		if character >= 'A' && character <= 'Z' {
			hasLetter = true
		}
	}
	return hasLetter
}

// CheckMarkdown checks the configured Markdown style boundary captured by the
// initial migration fixtures. Remaining active rules are added before the
// checker replaces markdownlint in Make or CI.
func CheckMarkdown(_ string, data []byte) []Diagnostic {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	diagnostics := make([]Diagnostic, 0)
	if len(data) != 0 && data[len(data)-1] != '\n' {
		diagnostics = append(diagnostics, newDiagnostic("MD047", len(lines), "file must end with one newline"))
	}

	headings := make(map[string]int)
	for index, line := range lines {
		lineNumber := index + 1
		if index > 0 && setextMarker.MatchString(line) && strings.TrimSpace(lines[index-1]) != "" {
			diagnostics = append(diagnostics, newDiagnostic("MD003", lineNumber, "heading style must be ATX"))
		}
		if match := listItem.FindStringSubmatch(line); match != nil && len(match[1]) != 2 {
			diagnostics = append(
				diagnostics,
				newDiagnostic("MD007", lineNumber, "nested unordered lists use two-space indentation"),
			)
		}
		if match := atxHeading.FindStringSubmatch(line); match != nil {
			key := match[1] + "\x00" + strings.ToLower(strings.TrimSpace(match[2]))
			if previous := headings[key]; previous != 0 {
				diagnostics = append(diagnostics, newDiagnostic("MD024", lineNumber, "duplicate sibling heading"))
			}
			headings[key] = lineNumber
		}
		if strings.HasPrefix(line, "    ") && strings.TrimSpace(line) != "" {
			diagnostics = append(diagnostics, newDiagnostic("MD046", lineNumber, "code blocks must be fenced"))
		}
		if strings.Contains(line, "__") {
			diagnostics = append(diagnostics, newDiagnostic("MD050", lineNumber, "strong emphasis uses asterisks"))
		}
	}
	return diagnostics
}
