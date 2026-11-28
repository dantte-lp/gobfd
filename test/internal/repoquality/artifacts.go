package repoquality

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	ruleNoBeadsArtifact = "no-beads-artifact"
	ruleNoAIArtifact    = "no-ai-artifact"
	ruleNoAIAttribution = "no-ai-attribution"
	markdownExtension   = ".md"

	beadsDirectoryList = ",.beads,.dolt,"

	// Directories whose entire subtree is local agent state (even Markdown).
	localOnlyAIDirectoryList = ",.serena,"

	// Directories where intentional Markdown (.md) is allowed, while all
	// non-Markdown configurations, scripts, databases, and state are forbidden.
	markdownOnlyAIDirectoryList = ",.agent,.agents,.amp,.antigravity,.augment,.claude,.cline," +
		".codex,.continue,.copilot,.cursor,.gemini,.goose,.junie,.kiro,.opencode,.roo,.trae,.windsurf,"

	githubMarkdownOnlySubdirList = ",agents,copilot,instructions,prompts,"

	forbiddenAIConfigNameList = ",.aiexclude,.aiignore,.ampignore,.claudeignore,.clineignore," +
		".clinerules,.codeiumignore,.codexignore,.continueignore,.continuerules,.copilotignore," +
		".cursorignore,.cursorindexingignore,.cursorrules,.geminiignore,.goosehints,.gooseignore," +
		".junieignore,.kiroignore,.mcp.json,.opencode.json,.opencode.jsonc,.rooignore,.roorules," +
		".windsurfignore,.windsurfrules,claude_desktop_config.json,opencode.json,opencode.jsonc,"

	aiIdentityTokenList = ",aider,ampcode,anthropic,antigravity,augment,chatgpt,claude,cline," +
		"codeium,codex,cody,copilot,cursor,cursoragent,deepseek,devin,gemini,google-labs-jules," +
		"grok,jules,junie,kiro,llm,mistral,openai,opencode,perplexity,qwen,roo,swe-agent,tabnine," +
		"trae,windsurf,"
)

var (
	errArtifactRootNotDirectory = errors.New("artifact policy root is not a directory")
	errNoTrackedFiles           = errors.New("no tracked Git files discovered")
)

// ArtifactReport contains the tracked Git file set and any merge-blocking
// tracker or non-Markdown AI artifact diagnostics.
type ArtifactReport struct {
	Files       []string
	Diagnostics []FileDiagnostic
}

// CheckGitTrackedFiles lists all Git-tracked paths under rootPath and rejects
// Beads/Dolt tracker state and non-Markdown AI artifacts.
func CheckGitTrackedFiles(ctx context.Context, rootPath string) (ArtifactReport, error) {
	return checkGitTrackedFiles(ctx, rootPath, osGoplsRunner{})
}

func checkGitTrackedFiles(ctx context.Context, rootPath string, runner goplsRunner) (ArtifactReport, error) {
	resolvedRoot, err := resolveDirectoryRoot(rootPath, "artifact")
	if err != nil {
		return ArtifactReport{}, err
	}
	result, err := runner.run(ctx, goplsCommand{
		executable:  "git",
		arguments:   []string{"-c", "safe.directory=*", "-c", "core.fsmonitor=false", "ls-files", "-z"},
		directory:   resolvedRoot,
		environment: os.Environ(),
	})
	if err != nil {
		return ArtifactReport{}, fmt.Errorf("git ls-files; output=%s: %w", commandOutput(result), err)
	}
	files := parseNullDelimitedPaths(result.stdout)
	if len(files) == 0 {
		return ArtifactReport{}, errNoTrackedFiles
	}
	return ArtifactReport{
		Files:       files,
		Diagnostics: CheckTrackedPaths(files),
	}, nil
}

func parseNullDelimitedPaths(raw string) []string {
	trimmed := strings.TrimSuffix(raw, "\x00")
	if trimmed == "" {
		return nil
	}
	files := make([]string, 0)
	for entry := range strings.SplitSeq(trimmed, "\x00") {
		if entry != "" {
			files = append(files, entry)
		}
	}
	slices.Sort(files)
	return files
}

// CheckTrackedPaths checks a slice of repository-relative paths for forbidden
// Beads/Dolt files and non-Markdown AI artifacts.
func CheckTrackedPaths(paths []string) []FileDiagnostic {
	diagnostics := make([]FileDiagnostic, 0, len(paths))
	for _, candidate := range paths {
		diagnostics = append(diagnostics, CheckTrackedPath(candidate)...)
	}
	slices.SortFunc(diagnostics, func(left, right FileDiagnostic) int {
		if order := strings.Compare(left.Path, right.Path); order != 0 {
			return order
		}
		return strings.Compare(left.Rule, right.Rule)
	})
	return diagnostics
}

// CheckTrackedPath evaluates one repository-relative path against the merge
// prohibition for Beads/Dolt state and non-Markdown AI artifacts.
func CheckTrackedPath(rawPath string) []FileDiagnostic {
	clean := normalizeTrackedPath(rawPath)
	if clean == "" {
		return nil
	}
	segments := strings.Split(clean, "/")
	base := segments[len(segments)-1]
	lowerBase := strings.ToLower(base)
	isMarkdown := strings.EqualFold(path.Ext(base), markdownExtension)

	if isBeadsOrDoltArtifact(segments, lowerBase) {
		return singleArtifactDiagnostic(clean, ruleNoBeadsArtifact,
			"Beads and Dolt tracker state must not be tracked or merged")
	}
	if isLocalOnlyAIArtifact(segments, lowerBase) {
		return singleArtifactDiagnostic(clean, ruleNoAIArtifact,
			"local AI agent state or override file must not be tracked or merged")
	}
	if inMarkdownOnlyAIDirectory(segments) {
		if isMarkdown {
			return nil
		}
		return singleArtifactDiagnostic(clean, ruleNoAIArtifact,
			"non-Markdown AI agent file must not be tracked or merged")
	}
	if !isMarkdown && isStandaloneNonMarkdownAIArtifact(lowerBase) {
		return singleArtifactDiagnostic(clean, ruleNoAIArtifact,
			"non-Markdown AI agent configuration must not be tracked or merged")
	}
	return nil
}

func normalizeTrackedPath(rawPath string) string {
	slashed := strings.TrimPrefix(strings.ReplaceAll(rawPath, "\\", "/"), "./")
	clean := path.Clean(slashed)
	if clean == "" || clean == "." || clean == "/" {
		return ""
	}
	return strings.TrimPrefix(clean, "/")
}

func singleArtifactDiagnostic(filePath, rule, message string) []FileDiagnostic {
	return []FileDiagnostic{{
		Diagnostic: newDiagnostic(rule, 1, message),
		Path:       filePath,
	}}
}

func isBeadsOrDoltArtifact(segments []string, lowerBase string) bool {
	for _, segment := range segments[:len(segments)-1] {
		if containsToken(beadsDirectoryList, strings.ToLower(segment)) {
			return true
		}
	}
	return strings.HasPrefix(lowerBase, ".beads") ||
		lowerBase == ".dolt" ||
		strings.HasSuffix(lowerBase, ".gate.lock") ||
		strings.Contains(lowerBase, ".gate.lock.")
}

func isLocalOnlyAIArtifact(segments []string, lowerBase string) bool {
	if strings.HasPrefix(lowerBase, ".aider") || strings.HasSuffix(lowerBase, ".local.md") {
		return true
	}
	for index, segment := range segments[:len(segments)-1] {
		lowerSegment := strings.ToLower(segment)
		if containsToken(localOnlyAIDirectoryList, lowerSegment) {
			return true
		}
		if lowerSegment == ".claude" && index+1 < len(segments)-1 &&
			strings.EqualFold(segments[index+1], "worktrees") {
			return true
		}
	}
	return false
}

func inMarkdownOnlyAIDirectory(segments []string) bool {
	directories := segments[:len(segments)-1]
	for _, segment := range directories {
		if containsToken(markdownOnlyAIDirectoryList, strings.ToLower(segment)) {
			return true
		}
	}
	return len(directories) >= 2 &&
		strings.EqualFold(directories[0], ".github") &&
		containsToken(githubMarkdownOnlySubdirList, strings.ToLower(directories[1]))
}

func isStandaloneNonMarkdownAIArtifact(lowerBase string) bool {
	if containsToken(forbiddenAIConfigNameList, lowerBase) ||
		containsToken(markdownOnlyAIDirectoryList, lowerBase) {
		return true
	}
	for _, suffix := range []string{
		".mdc",
		".prompt.yml", ".prompt.yaml", ".prompt.json",
		".agent.yml", ".agent.yaml", ".agent.json",
	} {
		if strings.HasSuffix(lowerBase, suffix) {
			return true
		}
	}
	return false
}

// CheckTextAIAttribution checks arbitrary commit or pull-request body text for
// forbidden AI attribution trailers and generator notices.
func CheckTextAIAttribution(text string) []Diagnostic {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	return checkCommitAIAttribution(lines)
}

// CheckGitRecentAttribution checks the recent Git commits reachable from HEAD
// and optional pullRequestBody text for forbidden AI attribution.
func CheckGitRecentAttribution(ctx context.Context, rootPath, pullRequestBody string) ([]FileDiagnostic, error) {
	return checkGitRecentAttribution(ctx, rootPath, pullRequestBody, osGoplsRunner{})
}

func resolveDirectoryRoot(rootPath, label string) (string, error) {
	if rootPath == "" {
		rootPath = "."
	}
	resolvedRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return "", fmt.Errorf("resolve %s policy root: %w", label, err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("stat %s policy root: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s policy root %s: %w", label, resolvedRoot, errArtifactRootNotDirectory)
	}
	return resolvedRoot, nil
}

func checkGitRecentAttribution(
	ctx context.Context,
	rootPath string,
	pullRequestBody string,
	runner goplsRunner,
) ([]FileDiagnostic, error) {
	resolvedRoot, err := resolveDirectoryRoot(rootPath, "attribution")
	if err != nil {
		return nil, err
	}
	result, err := runner.run(ctx, goplsCommand{
		executable: "git",
		arguments: []string{
			"-c", "safe.directory=*", "-c", "core.fsmonitor=false",
			"log", "-n", "5", "--format=%B", "HEAD",
		},
		directory:   resolvedRoot,
		environment: os.Environ(),
	})
	if err != nil {
		return nil, fmt.Errorf("git log HEAD; output=%s: %w", commandOutput(result), err)
	}
	commitDiagnostics := CheckTextAIAttribution(result.stdout)
	bodyDiagnostics := CheckTextAIAttribution(pullRequestBody)
	diagnostics := make([]FileDiagnostic, 0, len(commitDiagnostics)+len(bodyDiagnostics))
	for _, diagnostic := range commitDiagnostics {
		diagnostics = append(diagnostics, FileDiagnostic{Diagnostic: diagnostic, Path: "git-log"})
	}
	for _, diagnostic := range bodyDiagnostics {
		diagnostics = append(diagnostics, FileDiagnostic{Diagnostic: diagnostic, Path: "pr-body"})
	}
	return diagnostics, nil
}

func checkCommitAIAttribution(lines []string) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	for index, line := range lines {
		if hasAIAttributionLine(line) {
			diagnostics = append(
				diagnostics,
				newDiagnostic(ruleNoAIAttribution, index+1, "AI attribution trailer or generator notice is forbidden"),
			)
		}
	}
	return diagnostics
}

func hasAIAttributionLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, prefix := range []string{"co-authored-by:", "assisted-by:", "authored-by:", "helped-by:"} {
		if rest, ok := strings.CutPrefix(lower, prefix); ok {
			return containsAnyAIToken(rest)
		}
	}
	for _, marker := range []string{"generated with ", "generated by "} {
		if _, rest, ok := strings.Cut(lower, marker); ok && containsAnyAIToken(rest) {
			return true
		}
	}
	return false
}

func containsAnyAIToken(text string) bool {
	for token := range strings.SplitSeq(strings.Trim(aiIdentityTokenList, ","), ",") {
		if containsWordOrSubtoken(text, token) {
			return true
		}
	}
	return false
}

func containsWordOrSubtoken(text, token string) bool {
	for offset := 0; ; {
		index := strings.Index(text[offset:], token)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(token)
		leftOK := start == 0 || !isAlphaNumeric(rune(text[start-1]))
		rightOK := end == len(text) || !isAlphaNumeric(rune(text[end]))
		if leftOK && rightOK {
			return true
		}
		offset = start + len(token)
	}
}
