// Package repoquality implements repository-owned commit and documentation
// quality policies without a Node.js runtime.
package repoquality

import (
	"regexp"
	"slices"
	"strings"
)

// Diagnostic identifies one repository policy violation.
type Diagnostic struct {
	Rule    string
	Line    int
	Message string
	level   severity
}

type severity uint8

const (
	warningSeverity severity = 1
	errorSeverity   severity = 2
)

var (
	commitHeader  = regexp.MustCompile(`^([a-z]+)(?:\(([a-z0-9-]+)\))?!?: (.+)$`)
	atxHeading    = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	listItem      = regexp.MustCompile(`^( *)([-*+]|[0-9]+\.)( +)(.*)$`)
	setextMarker  = regexp.MustCompile(`^(?:=+|-+)[ \t]*$`)
	linkRef       = regexp.MustCompile(`\[[^]]+\]\[([^]]*)\]`)
	linkDef       = regexp.MustCompile(`^ {0,3}\[([^]]+)\]:\s*\S+`)
	tableDivider  = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)+\|?\s*$`)
	missingATX    = regexp.MustCompile(`^#{1,6}[^# \t]`)
	indentedATX   = regexp.MustCompile(`^ +#{1,6}(?: |$)`)
	blockquoteGap = regexp.MustCompile(`^> {2,}`)
	linkSpace     = regexp.MustCompile(`!?\[ +[^]]|[^[] +\]\(`)
	emptyLink     = regexp.MustCompile(`!?\[[^]]*\]\((?:\s*|#)\)`)
	emptyImage    = regexp.MustCompile(`!\[\]\(`)
	shortcutLink  = regexp.MustCompile(`\[([^]]+)\]`)
	collapsedLink = regexp.MustCompile(`!?\[([^]]+)\]\[\]`)

	reversedLinkPattern          = regexp.MustCompile(`\([^()]+\)\[[^]]+\]`)
	closedATXMissingSpacePattern = regexp.MustCompile(`^#{1,6} [^#].*[^ \t]#{1,6}[ \t]*$`)
	nondescriptiveLinkPattern    = regexp.MustCompile(`\[([^]]+)\]\([^)]*\)`)
)

const (
	headerMaxBytes     = 100
	bodyMaxBytes       = 120
	minimumFenceLength = 3
	decimalRadix       = 10
	strongMarkerLength = 2

	allowedCommitTypes  = ",build,chore,ci,docs,feat,fix,perf,refactor,revert,style,test,"
	allowedCommitScopes = ",api,bfd,build,ci,cli,config,deps,docs,examples,gobgp,interop,lint," +
		"metrics,netio,release,sdnotify,security,server,test,"
	markdownRuleList = "MD003,MD004,MD005,MD007,MD009,MD010,MD011,MD012,MD014," +
		"MD018,MD020,MD023,MD024,MD026,MD027,MD028,MD029,MD030," +
		"MD035,MD037,MD038,MD039,MD042,MD043,MD044,MD045,MD046," +
		"MD047,MD048,MD050,MD052,MD053,MD054,MD055,MD056,MD059"
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
				newWarning("body-max-line-length", index+2, "body line exceeds 120 bytes"),
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
	return Diagnostic{Rule: rule, Line: line, Message: message, level: errorSeverity}
}

func newWarning(rule string, line int, message string) Diagnostic {
	return Diagnostic{Rule: rule, Line: line, Message: message, level: warningSeverity}
}

// HasErrors reports whether diagnostics contain a blocking policy error.
func HasErrors(diagnostics []Diagnostic) bool {
	return slices.ContainsFunc(diagnostics, func(diagnostic Diagnostic) bool {
		return diagnostic.level == errorSeverity
	})
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

// CheckMarkdown checks the exact enabled repository Markdown policy. It is a
// line-oriented policy checker, not a general Markdown renderer.
func CheckMarkdown(_ string, data []byte) []Diagnostic {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	checker := newMarkdownChecker(lines)
	if len(data) != 0 && data[len(data)-1] != '\n' {
		checker.add("MD047", len(lines), "file must end with one newline")
	}
	for index, line := range lines {
		checker.checkLine(index, line)
	}
	return checker.finish()
}

type markdownChecker struct {
	lines                 []string
	visibleLines          []string
	diagnostics           []Diagnostic
	headings              map[string]int
	headingPath           []string
	definitions           map[string]int
	references            map[string]int
	shortcutReferences    map[string]int
	listIndents           map[int]int
	listMarkers           map[int]int
	unorderedMarker       string
	horizontalRule        string
	fenceStyle            string
	openFenceMarker       string
	fenceLength           int
	fenceStart            int
	blankRun              int
	previousOrderedIndent int
	previousOrderedNumber int
	inFence               bool
	fenceCommandsOnly     bool
	fenceHasCommand       bool
	frontMatter           bool
}

func newMarkdownChecker(lines []string) *markdownChecker {
	return &markdownChecker{
		lines:                 lines,
		visibleLines:          slices.Clone(lines),
		diagnostics:           make([]Diagnostic, 0),
		headings:              make(map[string]int),
		headingPath:           make([]string, 7),
		definitions:           make(map[string]int),
		references:            make(map[string]int),
		shortcutReferences:    make(map[string]int),
		listIndents:           make(map[int]int),
		listMarkers:           make(map[int]int),
		previousOrderedIndent: -1,
		fenceCommandsOnly:     true,
		frontMatter:           hasFrontMatter(lines),
	}
}

func (checker *markdownChecker) checkLine(index int, line string) {
	lineNumber := index + 1
	trimmed := strings.TrimSpace(line)
	if checker.skipProtected(index, lineNumber, line, trimmed) {
		return
	}
	visible := maskCodeSpans(line)
	checker.visibleLines[index] = visible
	checker.checkWhitespace(index, lineNumber, line, trimmed)
	checker.checkListAndStructure(index, lineNumber, line, trimmed, visible)
	checker.checkHeading(lineNumber, line)
	checker.checkBlock(lineNumber, line, trimmed)
	checker.checkInline(lineNumber, line, visible)
	checker.recordReferences(lineNumber, visible)
}

func (checker *markdownChecker) skipProtected(index, lineNumber int, line, trimmed string) bool {
	if checker.frontMatter {
		checker.visibleLines[index] = ""
		if index > 0 && trimmed == "---" {
			checker.frontMatter = false
		}
		return true
	}
	marker, length, closing, fence := markdownFence(line)
	if fence {
		checker.visibleLines[index] = ""
		checker.blankRun = 0
		checker.updateFence(lineNumber, marker, length, closing)
		return true
	}
	if !checker.inFence {
		return false
	}
	checker.visibleLines[index] = ""
	checker.recordFenceContent(trimmed)
	return true
}

func (checker *markdownChecker) updateFence(lineNumber int, marker string, length int, closing bool) {
	if !checker.inFence {
		checker.openFence(lineNumber, marker, length)
		return
	}
	if marker != checker.openFenceMarker || length < checker.fenceLength || !closing {
		return
	}
	if checker.fenceHasCommand && checker.fenceCommandsOnly {
		checker.add("MD014", checker.fenceStart, "shell commands must show output")
	}
	checker.inFence = false
}

func (checker *markdownChecker) openFence(lineNumber int, marker string, length int) {
	checker.inFence = true
	checker.fenceStart = lineNumber
	checker.fenceLength = length
	checker.openFenceMarker = marker
	checker.fenceCommandsOnly = true
	checker.fenceHasCommand = false
	if checker.fenceStyle == "" {
		checker.fenceStyle = marker
		return
	}
	if checker.fenceStyle != marker {
		checker.add("MD048", lineNumber, "fenced code style is inconsistent")
	}
}

func (checker *markdownChecker) recordFenceContent(trimmed string) {
	if trimmed == "" {
		return
	}
	if strings.HasPrefix(trimmed, "$ ") {
		checker.fenceHasCommand = true
		return
	}
	checker.fenceCommandsOnly = false
}

func (checker *markdownChecker) checkWhitespace(index, lineNumber int, line, trimmed string) {
	if trimmed == "" && index != len(checker.lines)-1 {
		checker.blankRun++
		if checker.blankRun > 1 {
			checker.add("MD012", lineNumber, "multiple consecutive blank lines")
		}
	} else {
		checker.blankRun = 0
	}
	trailing := len(line) - len(strings.TrimRight(line, " "))
	if trailing != 0 && trailing != 2 {
		checker.add("MD009", lineNumber, "trailing spaces must be zero or two")
	}
	if strings.ContainsRune(line, '\t') {
		checker.add("MD010", lineNumber, "hard tabs are not allowed")
	}
}

func (checker *markdownChecker) checkListAndStructure(index, lineNumber int, line, trimmed, visible string) {
	if reversedLink(visible) {
		checker.add("MD011", lineNumber, "reversed link syntax")
	}
	if index > 0 && setextMarker.MatchString(line) && strings.TrimSpace(checker.lines[index-1]) != "" {
		checker.add("MD003", lineNumber, "heading style must be ATX")
	}
	match := listItem.FindStringSubmatch(line)
	if match != nil {
		checker.diagnostics = checkListLine(checker.diagnostics, match, lineNumber, &checker.unorderedMarker,
			checker.listIndents, checker.listMarkers, &checker.previousOrderedIndent, &checker.previousOrderedNumber)
		return
	}
	if trimmed != "" {
		checker.previousOrderedIndent = -1
		checker.previousOrderedNumber = 0
	}
}

func (checker *markdownChecker) checkHeading(lineNumber int, line string) {
	if missingATX.MatchString(line) {
		checker.add("MD018", lineNumber, "ATX heading requires a space")
	}
	if closedATXMissingSpace(line) {
		checker.add("MD020", lineNumber, "closed ATX heading requires inner spaces")
	}
	if indentedATX.MatchString(line) {
		checker.add("MD023", lineNumber, "heading must start at the beginning of the line")
	}
	match := atxHeading.FindStringSubmatch(line)
	if match == nil {
		return
	}
	level := len(match[1])
	content := strings.ToLower(strings.TrimSpace(match[2]))
	key := strings.Join(checker.headingPath[1:level], "\x00") + "\x00" + match[1] + "\x00" + content
	if checker.headings[key] != 0 {
		checker.add("MD024", lineNumber, "duplicate sibling heading")
	}
	checker.headings[key] = lineNumber
	checker.headingPath[level] = content
	clear(checker.headingPath[level+1:])
	if strings.ContainsRune(".,;:!。，；：！", lastRune(strings.TrimSpace(match[2]))) {
		checker.add("MD026", lineNumber, "heading ends with punctuation")
	}
}

func (checker *markdownChecker) checkBlock(lineNumber int, line, trimmed string) {
	if blockquoteGap.MatchString(line) {
		checker.add("MD027", lineNumber, "blockquote marker has multiple spaces")
	}
	index := lineNumber - 1
	if trimmed == "" && index > 0 && index+1 < len(checker.lines) &&
		strings.HasPrefix(strings.TrimSpace(checker.lines[index-1]), ">") &&
		strings.HasPrefix(strings.TrimSpace(checker.lines[index+1]), ">") {
		checker.add("MD028", lineNumber, "blank line inside blockquote")
	}
	marker := horizontalRuleMarker(trimmed)
	if marker == "" {
		return
	}
	if checker.horizontalRule == "" {
		checker.horizontalRule = marker
		return
	}
	if checker.horizontalRule != marker {
		checker.add("MD035", lineNumber, "horizontal rule style is inconsistent")
	}
}

func (checker *markdownChecker) checkInline(lineNumber int, line, visible string) {
	checks := []struct {
		rule    string
		message string
		failed  bool
	}{
		{rule: "MD037", message: "spaces inside emphasis markers", failed: emphasisHasInnerSpace(visible)},
		{rule: "MD038", message: "spaces inside code span", failed: codeSpanHasInnerSpace(line)},
		{rule: "MD039", message: "spaces inside link text", failed: linkSpace.MatchString(visible)},
		{rule: "MD042", message: "link text is empty", failed: emptyLink.MatchString(visible)},
		{rule: "MD045", message: "image alt text is empty", failed: emptyImage.MatchString(visible)},
		{rule: "MD050", message: "strong emphasis uses asterisks", failed: underscoreStrong(visible)},
		{rule: "MD059", message: "link text is not descriptive", failed: nondescriptiveLink(visible)},
	}
	for _, check := range checks {
		if check.failed {
			checker.add(check.rule, lineNumber, check.message)
		}
	}
	index := lineNumber - 1
	if strings.HasPrefix(line, "    ") && strings.TrimSpace(line) != "" && index > 0 &&
		strings.TrimSpace(checker.lines[index-1]) == "" {
		checker.add("MD046", lineNumber, "code blocks must be fenced")
	}
}

func (checker *markdownChecker) recordReferences(lineNumber int, visible string) {
	if match := linkDef.FindStringSubmatch(visible); match != nil {
		checker.definitions[normalizeReference(match[1])] = lineNumber
	}
	for _, match := range linkRef.FindAllStringSubmatch(visible, -1) {
		identifier := match[1]
		if identifier == "" {
			collapsed := collapsedLink.FindStringSubmatch(match[0])
			if collapsed == nil {
				continue
			}
			identifier = collapsed[1]
		}
		checker.references[normalizeReference(identifier)] = lineNumber
	}
	for _, match := range shortcutLink.FindAllStringSubmatch(visible, -1) {
		if !strings.HasPrefix(visible, match[0]+":") {
			checker.shortcutReferences[normalizeReference(match[1])] = lineNumber
		}
	}
}

func (checker *markdownChecker) finish() []Diagnostic {
	for identifier, line := range checker.shortcutReferences {
		if _, ok := checker.definitions[identifier]; ok {
			checker.references[identifier] = line
		}
	}
	checker.diagnostics = append(checker.diagnostics, checkReferences(checker.definitions, checker.references)...)
	checker.diagnostics = append(checker.diagnostics, checkTables(checker.visibleLines)...)
	return checker.diagnostics
}

func (checker *markdownChecker) add(rule string, line int, message string) {
	checker.diagnostics = append(checker.diagnostics, newDiagnostic(rule, line, message))
}

func hasFrontMatter(lines []string) bool {
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return true
		}
	}
	return false
}

func markdownFence(line string) (string, int, bool, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 {
		return "", 0, false, false
	}
	marker := trimmed[0]
	if marker != '`' && marker != '~' {
		return "", 0, false, false
	}
	length := 1
	for length < len(trimmed) && trimmed[length] == marker {
		length++
	}
	if length < minimumFenceLength {
		return "", 0, false, false
	}
	rest := strings.TrimSpace(trimmed[length:])
	return string(marker), length, rest == "", true
}

func checkListLine(diagnostics []Diagnostic, match []string, lineNumber int, unorderedMarker *string,
	listIndents, listMarkers map[int]int, previousOrderedIndent, previousOrderedNumber *int,
) []Diagnostic {
	indent := len(match[1])
	marker := match[2]
	spaces := len(match[3])
	if marker == "-" || marker == "*" || marker == "+" {
		if *unorderedMarker == "" {
			*unorderedMarker = marker
		} else if *unorderedMarker != marker {
			diagnostics = append(diagnostics, newDiagnostic("MD004", lineNumber, "unordered list marker is inconsistent"))
		}
		if indent > 0 && indent != expectedListIndent(indent, listMarkers) {
			diagnostics = append(
				diagnostics,
				newDiagnostic("MD007", lineNumber, "nested unordered lists use two-space indentation"),
			)
		}
	}
	listMarkers[indent] = len(marker)
	if prior, ok := listIndents[indent/2]; ok && prior != indent {
		diagnostics = append(diagnostics, newDiagnostic("MD005", lineNumber, "list indentation is inconsistent"))
	} else {
		listIndents[indent/2] = indent
	}
	if spaces != 1 {
		diagnostics = append(diagnostics, newDiagnostic("MD030", lineNumber, "list marker requires one space"))
	}
	if numberText, ordered := strings.CutSuffix(marker, "."); ordered {
		number := parseDecimal(numberText)
		if *previousOrderedIndent == indent && number != *previousOrderedNumber+1 {
			diagnostics = append(diagnostics, newDiagnostic("MD029", lineNumber, "ordered list prefix is out of sequence"))
		}
		*previousOrderedIndent = indent
		*previousOrderedNumber = number
	} else {
		*previousOrderedIndent = -1
		*previousOrderedNumber = 0
	}
	return diagnostics
}

func expectedListIndent(indent int, listMarkers map[int]int) int {
	parentIndent := -1
	parentMarker := 1
	for candidate, marker := range listMarkers {
		if candidate < indent && candidate > parentIndent {
			parentIndent = candidate
			parentMarker = marker
		}
	}
	if parentIndent < 0 {
		return 0
	}
	return parentIndent + parentMarker + 1
}

func parseDecimal(value string) int {
	result := 0
	for _, character := range value {
		result = result*decimalRadix + int(character-'0')
	}
	return result
}

func reversedLink(line string) bool {
	return reversedLinkPattern.MatchString(line)
}

func closedATXMissingSpace(line string) bool {
	return closedATXMissingSpacePattern.MatchString(line)
}

func lastRune(value string) rune {
	var result rune
	for _, character := range value {
		result = character
	}
	return result
}

func horizontalRuleMarker(line string) string {
	compact := strings.ReplaceAll(line, " ", "")
	if len(compact) < minimumFenceLength {
		return ""
	}
	marker := compact[0]
	if marker != '*' && marker != '-' && marker != '_' {
		return ""
	}
	if strings.Trim(compact, string(marker)) == "" {
		return string(marker)
	}
	return ""
}

func emphasisHasInnerSpace(line string) bool {
	for _, marker := range []string{"***", "___", "**", "__", "*", "_"} {
		if emphasisMarkerHasInnerSpace(line, marker) {
			return true
		}
	}
	return false
}

func emphasisMarkerHasInnerSpace(line, marker string) bool {
	for offset := 0; ; {
		index := strings.Index(line[offset:], marker+" ")
		if index < 0 {
			return false
		}
		start := offset + index
		if emphasisCanOpen(line, start) && emphasisHasClosingMarker(line[start+len(marker)+1:], marker) {
			return true
		}
		offset = start + len(marker)
	}
}

func emphasisCanOpen(line string, start int) bool {
	return start == 0 || line[start-1] == ' ' || line[start-1] == '\t'
}

func emphasisHasClosingMarker(remainder, marker string) bool {
	for {
		end := strings.Index(remainder, " "+marker)
		if end < 0 {
			return false
		}
		after := end + 1 + len(marker)
		if after == len(remainder) || emphasisCanCloseBefore(remainder[after]) {
			return true
		}
		remainder = remainder[after:]
	}
}

func emphasisCanCloseBefore(character byte) bool {
	return character == ' ' || character == '\t' || strings.ContainsRune(".,;:!?)]}", rune(character))
}

func codeSpanHasInnerSpace(line string) bool {
	if countBacktickRuns(line)%2 != 0 {
		return false
	}
	for _, content := range codeSpanContents(line) {
		if strings.TrimSpace(content) == "" {
			continue
		}
		leading := len(content) - len(strings.TrimLeft(content, " "))
		trailing := len(content) - len(strings.TrimRight(content, " "))
		if leading != 0 || trailing != 0 {
			if leading == 1 && trailing == 1 {
				continue
			}
			return true
		}
	}
	return false
}

func countBacktickRuns(line string) int {
	count := 0
	for index := 0; index < len(line); {
		if line[index] != '`' {
			index++
			continue
		}
		count++
		for index < len(line) && line[index] == '`' {
			index++
		}
	}
	return count
}

func maskCodeSpans(line string) string {
	masked := []byte(line)
	for _, span := range codeSpans(line) {
		for index := span[0]; index < span[1]; index++ {
			masked[index] = 'x'
		}
	}
	return string(masked)
}

func codeSpanContents(line string) []string {
	spans := codeSpans(line)
	contents := make([]string, 0, len(spans))
	for _, span := range spans {
		run := 1
		for span[0]+run < span[1] && line[span[0]+run] == '`' {
			run++
		}
		contents = append(contents, line[span[0]+run:span[1]-run])
	}
	return contents
}

func codeSpans(line string) [][2]int {
	spans := make([][2]int, 0)
	for offset := 0; offset < len(line); {
		start := strings.IndexByte(line[offset:], '`')
		if start < 0 {
			break
		}
		start += offset
		run := 1
		for start+run < len(line) && line[start+run] == '`' {
			run++
		}
		end := strings.Index(line[start+run:], strings.Repeat("`", run))
		if end < 0 {
			break
		}
		end += start + run
		spans = append(spans, [2]int{start, end + run})
		offset = end + run
	}
	return spans
}

func underscoreStrong(line string) bool {
	for offset := 0; ; {
		index := strings.Index(line[offset:], "__")
		if index < 0 {
			return false
		}
		start := offset + index
		if (start == 0 || !isAlphaNumeric(rune(line[start-1]))) &&
			strings.Contains(line[start+2:], "__") {
			return true
		}
		offset = start + strongMarkerLength
	}
}

func isAlphaNumeric(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}

func normalizeReference(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func nondescriptiveLink(line string) bool {
	match := nondescriptiveLinkPattern.FindStringSubmatch(line)
	if match == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(match[1])) {
	case "click here", "here", "link", "more", "more here", "read more":
		return true
	default:
		return false
	}
}

func checkReferences(definitions, references map[string]int) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	for identifier, line := range references {
		if _, ok := definitions[identifier]; !ok {
			diagnostics = append(diagnostics, newDiagnostic("MD052", line, "reference is not defined"))
		}
	}
	for identifier, line := range definitions {
		if _, ok := references[identifier]; !ok {
			diagnostics = append(diagnostics, newDiagnostic("MD053", line, "reference is not used"))
		}
	}
	return diagnostics
}

func checkTables(lines []string) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	style := ""
	for index, line := range lines {
		if !tableDivider.MatchString(line) || index == 0 || index+1 >= len(lines) {
			continue
		}
		currentStyle := tablePipeStyle(lines[index-1])
		if style == "" {
			style = currentStyle
		} else if style != currentStyle {
			diagnostics = append(diagnostics, newDiagnostic("MD055", index, "table pipe style is inconsistent"))
		}
		for row := index; row <= index+1; row++ {
			if tablePipeStyle(lines[row]) != currentStyle {
				diagnostics = append(diagnostics, newDiagnostic("MD055", row+1, "table pipe style is inconsistent"))
			}
		}
		want := tableColumnCount(line)
		for row := index - 1; row <= index+1; row++ {
			if got := tableColumnCount(lines[row]); got != want {
				diagnostics = append(diagnostics, newDiagnostic("MD056", row+1, "table column count is inconsistent"))
			}
		}
	}
	return diagnostics
}

func tablePipeStyle(line string) string {
	trimmed := strings.TrimSpace(line)
	return strings.Join([]string{
		boolToken(strings.HasPrefix(trimmed, "|")),
		boolToken(strings.HasSuffix(trimmed, "|")),
	}, ":")
}

func boolToken(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func tableColumnCount(line string) int {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	return strings.Count(trimmed, "|") + 1
}
