package repoquality

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckTrackedPathPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		wantRule string
	}{
		{name: "root AGENTS markdown allowed", path: "AGENTS.md"},
		{name: "root CLAUDE markdown allowed", path: "CLAUDE.md"},
		{name: "root GEMINI markdown allowed", path: "GEMINI.md"},
		{name: "claude command markdown allowed", path: ".claude/commands/review.md"},
		{name: "agents skill markdown allowed", path: ".agents/skills/bfd/SKILL.md"},
		{name: "cursor markdown allowed", path: ".cursor/rules/architecture.md"},
		{name: "codex markdown allowed", path: ".codex/instructions.md"},
		{name: "gemini markdown allowed", path: ".gemini/styleguide.md"},
		{name: "github copilot markdown allowed", path: ".github/copilot-instructions.md"},
		{name: "github instructions markdown allowed", path: ".github/instructions/go.instructions.md"},
		{name: "github agents markdown allowed", path: ".github/agents/reviewer.md"},
		{name: "github prompts markdown allowed", path: ".github/prompts/audit.md"},
		{name: "github workflow yaml allowed", path: ".github/workflows/ci.yml"},
		{name: "github dependabot allowed", path: ".github/dependabot.yml"},
		{name: "haproxy agent go source allowed", path: "cmd/gobfd-haproxy-agent/main.go"},
		{name: "haproxy agent config allowed", path: "deployments/integrations/haproxy-health/haproxy-agent.yml"},
		{name: "docs local topic markdown allowed", path: "docs/en/local.md"},

		{name: "beads config forbidden", path: ".beads/config.yaml", wantRule: ruleNoBeadsArtifact},
		{name: "beads issues jsonl forbidden", path: ".beads/issues.jsonl", wantRule: ruleNoBeadsArtifact},
		{name: "beads markdown also forbidden", path: ".beads/README.md", wantRule: ruleNoBeadsArtifact},
		{name: "nested beads markdown forbidden", path: "tools/.beads/notes.md", wantRule: ruleNoBeadsArtifact},
		{name: "beads gate lock forbidden", path: ".beads.gate.lock", wantRule: ruleNoBeadsArtifact},
		{name: "beads credential key forbidden", path: ".beads-credential-key", wantRule: ruleNoBeadsArtifact},
		{name: "dolt directory forbidden", path: ".dolt/noms/manifest", wantRule: ruleNoBeadsArtifact},
		{name: "generic gate lock forbidden", path: "session.gate.lock", wantRule: ruleNoBeadsArtifact},

		{name: "claude local markdown forbidden", path: "CLAUDE.local.md", wantRule: ruleNoAIArtifact},
		{name: "agents local markdown forbidden", path: "AGENTS.local.md", wantRule: ruleNoAIArtifact},
		{name: "gemini local markdown forbidden", path: "GEMINI.local.md", wantRule: ruleNoAIArtifact},
		{name: "junie local markdown forbidden", path: "JUNIE.local.md", wantRule: ruleNoAIArtifact},
		{name: "roo local markdown forbidden", path: "ROO.local.md", wantRule: ruleNoAIArtifact},
		{name: "goose markdown allowed", path: ".goose/instructions.md"},
		{name: "goose yaml config forbidden", path: ".goose/config.yaml", wantRule: ruleNoAIArtifact},
		{name: "nested claude local markdown forbidden", path: ".claude/settings.local.md", wantRule: ruleNoAIArtifact},
		{name: "claude worktree markdown forbidden", path: ".claude/worktrees/w1/README.md", wantRule: ruleNoAIArtifact},
		{name: "serena yaml forbidden", path: ".serena/project.yml", wantRule: ruleNoAIArtifact},
		{name: "serena memory markdown forbidden", path: ".serena/memories/notes.md", wantRule: ruleNoAIArtifact},
		{name: "aider config forbidden", path: ".aider.conf.yml", wantRule: ruleNoAIArtifact},
		{name: "aider history markdown forbidden", path: ".aider.chat.history.md", wantRule: ruleNoAIArtifact},

		{name: "claude settings json forbidden", path: ".claude/settings.json", wantRule: ruleNoAIArtifact},
		{name: "codex config toml forbidden", path: ".codex/config.toml", wantRule: ruleNoAIArtifact},
		{name: "gemini settings json forbidden", path: ".gemini/settings.json", wantRule: ruleNoAIArtifact},
		{name: "agents python script forbidden", path: ".agents/skills/bfd/check.py", wantRule: ruleNoAIArtifact},
		{name: "agent yaml forbidden", path: ".agent/workflows/build.yaml", wantRule: ruleNoAIArtifact},
		{name: "antigravity state json forbidden", path: ".antigravity/config.json", wantRule: ruleNoAIArtifact},
		{name: "cursor mcp json forbidden", path: ".cursor/mcp.json", wantRule: ruleNoAIArtifact},
		{name: "cursor mdc rule forbidden", path: ".cursor/rules/go.mdc", wantRule: ruleNoAIArtifact},
		{name: "windsurf json forbidden", path: ".windsurf/settings.json", wantRule: ruleNoAIArtifact},
		{name: "opencode dir config forbidden", path: ".opencode/package.json", wantRule: ruleNoAIArtifact},
		{name: "roo mcp json forbidden", path: ".roo/mcp.json", wantRule: ruleNoAIArtifact},
		{name: "cline config forbidden", path: ".cline/config.json", wantRule: ruleNoAIArtifact},
		{name: "continue yaml forbidden", path: ".continue/config.yaml", wantRule: ruleNoAIArtifact},
		{name: "copilot dir json forbidden", path: ".copilot/mcp-config.json", wantRule: ruleNoAIArtifact},
		{name: "junie yaml forbidden", path: ".junie/guidelines.yaml", wantRule: ruleNoAIArtifact},
		{name: "amp settings forbidden", path: ".amp/settings.json", wantRule: ruleNoAIArtifact},
		{name: "kiro spec json forbidden", path: ".kiro/specs/design.json", wantRule: ruleNoAIArtifact},
		{name: "trae json forbidden", path: ".trae/rules.json", wantRule: ruleNoAIArtifact},
		{name: "augment json forbidden", path: ".augment/config.json", wantRule: ruleNoAIArtifact},
		{name: "github copilot non-md forbidden", path: ".github/copilot/mcp.json", wantRule: ruleNoAIArtifact},
		{name: "github agents non-md forbidden", path: ".github/agents/reviewer.yml", wantRule: ruleNoAIArtifact},
		{name: "github prompts non-md forbidden", path: ".github/prompts/check.yml", wantRule: ruleNoAIArtifact},
		{name: "github instructions non-md forbidden", path: ".github/instructions/go.json", wantRule: ruleNoAIArtifact},

		{name: "root mcp json forbidden", path: ".mcp.json", wantRule: ruleNoAIArtifact},
		{name: "dot codex plain file forbidden", path: ".codex", wantRule: ruleNoAIArtifact},
		{name: "nested mcp json forbidden", path: "deployments/.mcp.json", wantRule: ruleNoAIArtifact},
		{name: "opencode json forbidden", path: "opencode.json", wantRule: ruleNoAIArtifact},
		{name: "dot opencode jsonc forbidden", path: ".opencode.jsonc", wantRule: ruleNoAIArtifact},
		{name: "claude desktop config forbidden", path: "claude_desktop_config.json", wantRule: ruleNoAIArtifact},
		{name: "cursorrules forbidden", path: ".cursorrules", wantRule: ruleNoAIArtifact},
		{name: "cursorignore forbidden", path: ".cursorignore", wantRule: ruleNoAIArtifact},
		{name: "windsurfrules forbidden", path: ".windsurfrules", wantRule: ruleNoAIArtifact},
		{name: "clinerules forbidden", path: ".clinerules", wantRule: ruleNoAIArtifact},
		{name: "roorules forbidden", path: ".roorules", wantRule: ruleNoAIArtifact},
		{name: "goosehints forbidden", path: ".goosehints", wantRule: ruleNoAIArtifact},
		{name: "aiignore forbidden", path: ".aiignore", wantRule: ruleNoAIArtifact},
		{name: "standalone mdc forbidden", path: "docs/rules.mdc", wantRule: ruleNoAIArtifact},
		{name: "prompt yaml forbidden", path: "audit.prompt.yaml", wantRule: ruleNoAIArtifact},
		{name: "agent yaml suffix forbidden", path: "reviewer.agent.yml", wantRule: ruleNoAIArtifact},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			diagnostics := CheckTrackedPath(test.path)
			if test.wantRule == "" {
				if len(diagnostics) != 0 {
					t.Fatalf("CheckTrackedPath(%q) = %#v, want no diagnostics", test.path, diagnostics)
				}
				return
			}
			if len(diagnostics) != 1 || diagnostics[0].Rule != test.wantRule {
				t.Fatalf("CheckTrackedPath(%q) = %#v, want single rule %s", test.path, diagnostics, test.wantRule)
			}
		})
	}
}

type stubArtifactRunner struct {
	stdout string
	err    error
}

func (runner stubArtifactRunner) run(_ context.Context, _ goplsCommand) (goplsCommandResult, error) {
	return goplsCommandResult{stdout: runner.stdout}, runner.err
}

func TestCheckGitTrackedFilesFailsClosedAndDetectsViolations(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	if _, err := checkGitTrackedFiles(t.Context(), directory, stubArtifactRunner{}); !errors.Is(err, errNoTrackedFiles) {
		t.Fatalf("empty git ls-files err = %v, want %v", err, errNoTrackedFiles)
	}

	cleanStdout := "AGENTS.md\x00README.md\x00cmd/gobfd/main.go\x00"
	cleanReport, err := checkGitTrackedFiles(t.Context(), directory, stubArtifactRunner{stdout: cleanStdout})
	if err != nil {
		t.Fatalf("clean tracked set error: %v", err)
	}
	if len(cleanReport.Files) != 3 || len(cleanReport.Diagnostics) != 0 {
		t.Fatalf("clean report = %#v, want 3 files and 0 diagnostics", cleanReport)
	}

	dirtyStdout := "AGENTS.md\x00.beads/issues.jsonl\x00.claude/settings.json\x00"
	dirtyReport, err := checkGitTrackedFiles(t.Context(), directory, stubArtifactRunner{stdout: dirtyStdout})
	if err != nil {
		t.Fatalf("dirty tracked set error: %v", err)
	}
	if len(dirtyReport.Diagnostics) != 2 {
		t.Fatalf("dirty diagnostics = %#v, want 2 violations", dirtyReport.Diagnostics)
	}
	if dirtyReport.Diagnostics[0].Rule != ruleNoBeadsArtifact || dirtyReport.Diagnostics[1].Rule != ruleNoAIArtifact {
		t.Fatalf("dirty diagnostics = %#v, want [%s, %s]", dirtyReport.Diagnostics, ruleNoBeadsArtifact, ruleNoAIArtifact)
	}

	attributionDiags, err := checkGitRecentAttribution(
		t.Context(),
		directory,
		"Summary\n\nGenerated with DeepSeek\n",
		stubArtifactRunner{stdout: "feat(bfd): add peer\n\nAssisted-by: Jules <jules@example.com>\n"},
	)
	if err != nil {
		t.Fatalf("checkGitRecentAttribution error: %v", err)
	}
	if len(attributionDiags) != 2 {
		t.Fatalf("attribution diagnostics = %#v, want 2 violations", attributionDiags)
	}
}

func TestRepositoryHasNoForbiddenTrackedArtifacts(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	report, err := CheckGitTrackedFiles(t.Context(), repositoryRoot)
	if err != nil {
		t.Fatalf("check tracked repository artifacts: %v", err)
	}
	if len(report.Files) == 0 {
		t.Fatal("CheckGitTrackedFiles inspected zero files")
	}
	for _, diagnostic := range report.Diagnostics {
		t.Errorf("%s:%d %s %s", diagnostic.Path, diagnostic.Line, diagnostic.Rule, diagnostic.Message)
	}
}

func TestCheckGitTrackedFilesRealGitRepository(t *testing.T) {
	t.Parallel()

	repoDir := t.TempDir()
	runner := osGoplsRunner{}
	if _, err := runner.run(t.Context(), goplsCommand{
		executable:  "git",
		arguments:   []string{"init", "-q"},
		directory:   repoDir,
		environment: os.Environ(),
	}); err != nil {
		t.Fatalf("git init: %v", err)
	}

	fixtures := []string{
		"AGENTS.md",
		".claude/commands/review.md",
		".beads/issues.jsonl",
		".claude/settings.json",
		"CLAUDE.local.md",
	}
	for _, rel := range fixtures {
		full := filepath.Join(repoDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte("# fixture\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if _, err := runner.run(t.Context(), goplsCommand{
		executable:  "git",
		arguments:   []string{"add", "-f", "--", "."},
		directory:   repoDir,
		environment: os.Environ(),
	}); err != nil {
		t.Fatalf("git add -f: %v", err)
	}

	report, err := CheckGitTrackedFiles(t.Context(), repoDir)
	if err != nil {
		t.Fatalf("CheckGitTrackedFiles: %v", err)
	}
	if len(report.Files) != len(fixtures) {
		t.Fatalf("tracked files = %v, want %d files", report.Files, len(fixtures))
	}
	if len(report.Diagnostics) != 3 {
		t.Fatalf("diagnostics = %#v, want 3 forbidden artifacts", report.Diagnostics)
	}
}
