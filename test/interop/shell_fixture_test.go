package interop_test

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrackedGoTestsContainNoShellShebangFixtures(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	paths, fallback, err := trackedOperationalPaths(t.Context(), root)
	if err != nil {
		t.Fatalf("discover repository paths: %v", err)
	}
	scanned, fixtures, scanErr := scanShellShebangFixtures(root, paths)
	if scanErr != nil {
		t.Fatalf("scan repository Go tests: %v", scanErr)
	}
	if scanned == 0 {
		t.Fatal("repository discovery returned no Go tests")
	}
	t.Logf("scanned %d Go tests; filesystem fallback=%t", scanned, fallback)
	if len(fixtures) != 0 {
		t.Fatalf("tracked Go tests contain %d shell-shebang fixtures:\n%s", len(fixtures), strings.Join(fixtures, "\n"))
	}
}

func scanShellShebangFixtures(root string, paths []string) (int, []string, error) {
	rooted, err := os.OpenRoot(root)
	if err != nil {
		return 0, nil, fmt.Errorf("open repository root: %w", err)
	}
	defer func() { _ = rooted.Close() }()

	var scanned int
	var fixtures []string
	for _, relative := range paths {
		if !strings.HasSuffix(relative, "_test.go") {
			continue
		}
		scanned++
		clean := filepath.Clean(filepath.FromSlash(relative))
		info, err := rooted.Lstat(clean)
		if err != nil {
			return 0, nil, fmt.Errorf("lstat Go test %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return 0, nil, fmt.Errorf("Go test %s is a symlink", relative)
		}
		if !info.Mode().IsRegular() {
			return 0, nil, fmt.Errorf("Go test %s is not a regular file", relative)
		}
		contents, err := readTrackedOperationalFile(rooted, clean, info)
		if err != nil {
			return 0, nil, fmt.Errorf("read Go test %s: %w", relative, err)
		}
		for line := range bytes.SplitSeq(contents, []byte{'\n'}) {
			if isShellShebangLine(line) {
				fixtures = append(fixtures, relative)
			}
		}
	}
	return scanned, fixtures, nil
}

func TestShellFixtureScanRejectsUnsafeFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		setup   func(*testing.T, string)
	}{
		{
			name:    "symlink",
			wantErr: "is a symlink",
			setup: func(t *testing.T, fixture string) {
				t.Helper()
				outside := filepath.Join(t.TempDir(), "outside.txt")
				if err := os.WriteFile(outside, []byte("#!"+"/bin/sh\n"), 0o600); err != nil {
					t.Fatalf("write outside fixture: %v", err)
				}
				if err := os.Symlink(outside, fixture); err != nil {
					t.Fatalf("create scanner symlink fixture: %v", err)
				}
			},
		},
		{
			name:    "oversize",
			wantErr: "limit is",
			setup: func(t *testing.T, fixture string) {
				t.Helper()
				contents := bytes.Repeat([]byte{'a'}, maxOperationalFileSize+1)
				if err := os.WriteFile(fixture, contents, 0o600); err != nil {
					t.Fatalf("write oversized scanner fixture: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			const relative = "fixture_test.go"
			test.setup(t, filepath.Join(root, relative))

			_, _, err := scanShellShebangFixtures(root, []string{relative})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("unsafe scanner error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestShellShebangLine(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		line string
		want bool
	}{
		"space after marker": {line: "#!" + " /bin/sh", want: true},
		"env shell":          {line: "#!" + "/usr/bin/env dash", want: true},
		"env split string":   {line: "#!" + "/usr/bin/env -S bash -eu", want: true},
		"dash":               {line: "#!" + "/bin/dash", want: true},
		"ksh":                {line: "#!" + "/usr/bin/ksh", want: true},
		"zsh":                {line: "#!" + "/usr/local/bin/zsh", want: true},
		"other interpreter":  {line: "#!" + "/usr/bin/python3"},
		"shell-like suffix":  {line: "#!" + "/bin/bashful"},
		"env other command":  {line: "#!" + "/usr/bin/env -S python3"},
		"escaped streams fixture": {
			line: `writeExecutable(t, fakeBin, "podman", "` + "#!" +
				`/bin/sh\nprintf 'stdout-value'\nprintf 'stderr-value' >&2\nexit 7\n")`,
			want: true,
		},
		"escaped oversized fixture": {
			line: `writeExecutable(t, fakeBin, "podman", "` + "#!" +
				`/bin/sh\n/usr/bin/head -c 1048577 /dev/zero\n")`,
			want: true,
		},
		"escaped cancellation fixture": {
			line: `writeExecutable(t, fakeBin, "podman", "` + "#!" +
				`/bin/sh\nwhile :; do :; done\n")`,
			want: true,
		},
		"quoted string terminator": {line: `const fixture = "` + "#!" + `/bin/sh"`, want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := isShellShebangLine([]byte(test.line)); got != test.want {
				t.Errorf("isShellShebangLine(%q) = %t, want %t", test.line, got, test.want)
			}
		})
	}
}

func isShellShebangLine(line []byte) bool {
	_, command, found := bytes.Cut(line, []byte("#!"))
	return found && isShellShebang(command)
}

func isShellShebang(command []byte) bool {
	if end := bytes.IndexAny(command, "\\\"`"); end >= 0 {
		command = command[:end]
	}
	fields := bytes.Fields(command)
	if len(fields) == 0 {
		return false
	}
	interpreter := string(fields[0])
	if path.IsAbs(interpreter) && isShellName(path.Base(interpreter)) {
		return true
	}
	if !path.IsAbs(interpreter) || path.Base(interpreter) != "env" || len(fields) < 2 {
		return false
	}
	shellIndex := 1
	if string(fields[shellIndex]) == "-S" {
		shellIndex++
	}
	if shellIndex >= len(fields) {
		return false
	}
	shell := string(fields[shellIndex])
	return isShellName(shell) || path.IsAbs(shell) && isShellName(path.Base(shell))
}

func isShellName(name string) bool {
	switch name {
	case "sh", "bash", "dash", "ksh", "zsh":
		return true
	default:
		return false
	}
}
