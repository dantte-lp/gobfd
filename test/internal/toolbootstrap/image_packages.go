package toolbootstrap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// InstallImagePackages prepares one fixed first-party runtime image in one layer.
func InstallImagePackages(ctx context.Context, profile string) error {
	if profile == "dev" {
		if err := removeImageAPTCachePolicy("/etc/apt/apt.conf.d/docker-clean"); err != nil {
			return err
		}
	}
	return installImagePackages(ctx, profile, "/var/lib/apt/lists", runStreaming)
}

func removeImageAPTCachePolicy(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect image APT cache policy: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("image APT cache policy is a directory: %w", os.ErrInvalid)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove image APT cache policy: %w", err)
	}
	return nil
}

func installImagePackages(
	ctx context.Context, profile, lists string,
	run func(context.Context, string, []string, []string) error,
) (err error) {
	var packages []string
	switch profile {
	case "dev":
		packages = []string{"git", "ca-certificates", "tzdata", "make", "gcc", "libc6-dev", "curl", "jq", "podman", "gnupg"}
	case "debug":
		packages = []string{"adduser", "dnsutils", "iproute2", "strace", "tcpdump"}
	case "containerlab":
		packages = []string{"ethtool", "iproute2", "tcpdump"}
	default:
		return fmt.Errorf("unknown image package profile %q: %w", profile, os.ErrInvalid)
	}
	defer func() { err = errors.Join(err, clearImageAPTLists(lists)) }()
	commands := [][]string{
		{aptGetCommand, "update"},
		append([]string{aptGetCommand, "install", "-y", "--no-install-recommends"}, packages...),
	}
	if profile == "debug" {
		commands = append(commands,
			[]string{"addgroup", "--system", "gobfd"},
			[]string{"adduser", "--system", "--ingroup", "gobfd", "--shell", "/usr/sbin/nologin", "--no-create-home", "gobfd"},
		)
	}
	for _, command := range commands {
		if runErr := run(ctx, command[0], command[1:], nil); runErr != nil {
			return fmt.Errorf("prepare %s image packages: %w", profile, runErr)
		}
	}
	return nil
}

func clearImageAPTLists(directory string) (err error) {
	root, openErr := os.OpenRoot(directory)
	if openErr != nil {
		return fmt.Errorf("open image APT lists: %w", openErr)
	}
	defer func() {
		err = errors.Join(err, wrapComposeCleanupError("close image APT lists", root.Close()))
	}()
	entries, readErr := fs.ReadDir(root.FS(), ".")
	if readErr != nil {
		return fmt.Errorf("read image APT lists: %w", readErr)
	}
	for _, entry := range entries {
		if removeErr := root.RemoveAll(entry.Name()); removeErr != nil {
			return fmt.Errorf("remove image APT list %s: %w", entry.Name(), removeErr)
		}
	}
	return nil
}
