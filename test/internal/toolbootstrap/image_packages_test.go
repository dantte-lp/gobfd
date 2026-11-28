package toolbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestImagePackagesStopOnCommandFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("package command failed")
	for _, test := range []struct {
		profile  string
		commands [][]string
	}{
		{"dev", [][]string{
			{"apt-get", "update"},
			{
				"apt-get", "install", "-y", "--no-install-recommends", "git", "ca-certificates", "tzdata", "make",
				"gcc", "libc6-dev", "curl", "jq", "podman", "gnupg",
			},
		}},
		{"debug", [][]string{
			{"apt-get", "update"},
			{"apt-get", "install", "-y", "--no-install-recommends", "adduser", "dnsutils", "iproute2", "strace", "tcpdump"},
			{"addgroup", "--system", "gobfd"},
			{"adduser", "--system", "--ingroup", "gobfd", "--shell", "/usr/sbin/nologin", "--no-create-home", "gobfd"},
		}},
		{"containerlab", [][]string{
			{"apt-get", "update"},
			{"apt-get", "install", "-y", "--no-install-recommends", "ethtool", "iproute2", "tcpdump"},
		}},
	} {
		t.Run(test.profile, func(t *testing.T) {
			t.Parallel()
			steps := len(test.commands)
			for failAt := 0; failAt <= steps; failAt++ {
				lists := t.TempDir()
				if err := os.WriteFile(filepath.Join(lists, "index"), []byte("index"), 0o600); err != nil {
					t.Fatal(err)
				}
				calls := 0
				run := func(_ context.Context, name string, arguments, _ []string) error {
					calls++
					if !slices.Equal(append([]string{name}, arguments...), test.commands[calls-1]) {
						t.Fatalf("command %d changed: %s %v", calls, name, arguments)
					}
					if calls == failAt {
						return failure
					}
					return nil
				}
				err := installImagePackages(t.Context(), test.profile, lists, run)
				wantCalls := steps
				if failAt != 0 {
					wantCalls = failAt
					if !errors.Is(err, failure) {
						t.Fatalf("failure %d lost: %v", failAt, err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if calls != wantCalls {
					t.Fatalf("failure %d ran %d commands, want %d", failAt, calls, wantCalls)
				}
				entries, readErr := os.ReadDir(lists)
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("APT lists not cleaned after failure %d: %v, %v", failAt, entries, readErr)
				}
			}
		})
	}
}

func TestImageAPTCachePolicyRemovalPreservesDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policy := filepath.Join(root, "docker-clean")
	if err := os.WriteFile(policy, []byte("cache policy"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := removeImageAPTCachePolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(policy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeImageAPTCachePolicy(policy); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("directory accepted as cache policy: %v", err)
	}
	if info, err := os.Stat(policy); err != nil || !info.IsDir() {
		t.Fatalf("cache-policy directory removed: %v", err)
	}
	parentFile := filepath.Join(root, "file")
	if err := os.WriteFile(parentFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeImageAPTCachePolicy(filepath.Join(parentFile, "child")); err == nil {
		t.Fatal("cache policy inspection error was ignored")
	}
}

func TestImagePackagesRejectUnknownProfileBeforeMutation(t *testing.T) {
	t.Parallel()
	lists := filepath.Join(t.TempDir(), "missing")
	run := func(context.Context, string, []string, []string) error {
		t.Fatal("unknown profile executed a command")
		return nil
	}
	if err := installImagePackages(t.Context(), "debug;exec", lists, run); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("unknown profile was not rejected: %v", err)
	}
}

func TestImageAPTListsCleanupStaysInsideRoot(t *testing.T) {
	t.Parallel()
	lists, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(lists, "partial"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lists, "partial", ".index"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(lists, "external")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := clearImageAPTLists(lists); err != nil {
			t.Fatal(err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(data) != "keep" {
		t.Fatalf("cleanup escaped lists root: %s, %v", data, err)
	}
	if entries, err := os.ReadDir(lists); err != nil || len(entries) != 0 {
		t.Fatalf("cleanup retained APT lists: %v, %v", entries, err)
	}
}

func TestImagePackagesReportCleanupFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("APT failed")
	run := func(context.Context, string, []string, []string) error { return failure }
	missing := filepath.Join(t.TempDir(), "missing")
	err := installImagePackages(t.Context(), "containerlab", missing, run)
	if !errors.Is(err, failure) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command or cleanup error lost: %v", err)
	}
}
