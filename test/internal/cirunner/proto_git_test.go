package cirunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestProtoVerifyRejectsGeneratedGitDrift(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		path      string
		stage     bool
		remove    bool
		wantDirty bool
	}{
		{name: "unchanged"},
		{name: "modified tracked", path: "pkg/bfdpb/baseline.pb.go", wantDirty: true},
		{name: "new generated", path: "pkg/bfdpb/new.pb.go", wantDirty: true},
		{name: "staged generated", path: "pkg/bfdpb/baseline.pb.go", stage: true, wantDirty: true},
		{name: "deleted tracked", path: "pkg/bfdpb/baseline.pb.go", remove: true, wantDirty: true},
		{name: "unrelated untracked", path: "notes.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			generatedDir := filepath.Join(root, "pkg", "bfdpb")
			if err := os.MkdirAll(generatedDir, 0o755); err != nil {
				t.Fatal(err)
			}
			baseline := filepath.Join(generatedDir, "baseline.pb.go")
			if err := os.WriteFile(baseline, []byte("package baseline\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"init", "--quiet"},
				{"add", "--", "pkg/bfdpb"},
				{
					"-c", "user.name=Proto Fixture", "-c", "user.email=fixture@example.invalid",
					"commit", "--quiet", "--no-gpg-sign", "-m", "fixture",
				},
			} {
				if err := (ExecRunner{}).RunCommand(t.Context(), CommandSpec{
					Name: "git", Args: args, Dir: root,
				}); err != nil {
					t.Fatalf("initialize Git fixture: %v", err)
				}
			}
			err := ProtoVerify(t.Context(), ProtoOptions{
				Root: root, RunnerTemp: t.TempDir(),
				Runner: protoGitRunner{path: tc.path, stage: tc.stage, remove: tc.remove},
			})
			if (err != nil) != tc.wantDirty {
				t.Fatalf("ProtoVerify() error = %v, want generated drift rejection = %t", err, tc.wantDirty)
			}
		})
	}
}

// Only external generation is replaced; verification runs real Git commands.
type protoGitRunner struct {
	path   string
	stage  bool
	remove bool
}

func (r protoGitRunner) RunCommand(ctx context.Context, spec CommandSpec) error {
	switch spec.Name {
	case "go":
		return nil
	case "buf":
		if r.path == "" {
			return nil
		}
		path := filepath.Join(spec.Dir, r.path)
		if r.remove {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove generated fixture: %w", err)
			}
		} else if err := os.WriteFile(path, []byte("package changed\n"), 0o644); err != nil {
			return fmt.Errorf("write generated fixture: %w", err)
		}
		if r.stage {
			return (ExecRunner{}).RunCommand(ctx, CommandSpec{
				Name: "git", Args: []string{"add", "--", r.path}, Dir: spec.Dir,
			})
		}
		return nil
	default:
		return (ExecRunner{}).RunCommand(ctx, spec)
	}
}
