// Command gobfd-package-lifecycle owns package-manager lifecycle actions.
//
// It is package-internal and is not a public GoBFD command. Packaging installs
// the same executable under the Debian maintainer-script and RPM interpreter
// aliases that select the required lifecycle phase.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	sysusersConfig = "/usr/lib/sysusers.d/gobfd.conf"
	tmpfilesConfig = "/usr/lib/tmpfiles.d/gobfd.conf"
	serviceUnit    = "gobfd"
)

var errMalformedInvocation = errors.New("malformed package lifecycle invocation")

type command struct {
	name            string
	args            []string
	optional        bool
	tolerateFailure bool
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(context.Background(), os.Args[0], os.Args[1:], logger); err != nil {
		logger.Error("package lifecycle failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, invocation string, args []string, logger *slog.Logger) error {
	commands, err := plan(invocation, args)
	if err != nil {
		return fmt.Errorf("plan package lifecycle: %w", err)
	}
	if err := execute(ctx, commands, logger); err != nil {
		return fmt.Errorf("execute package lifecycle: %w", err)
	}
	return nil
}

func plan(invocation string, args []string) ([]command, error) {
	name := filepath.Base(invocation)
	switch {
	case strings.HasSuffix(name, ".postinst"):
		return planDebianPostInstall(name, args)
	case strings.HasSuffix(name, ".prerm"):
		return planDebianPreRemove(name, args)
	case name == "gobfd-postinstall", name == "gobfd-preremove":
		return planRPM(name, args)
	default:
		return nil, fmt.Errorf("%w: unknown executable %q", errMalformedInvocation, name)
	}
}

func planDebianPostInstall(name string, args []string) ([]command, error) {
	if len(args) != 2 || args[0] != "configure" {
		return nil, fmt.Errorf("%w: %s expects configure old-version", errMalformedInvocation, name)
	}
	return installCommands(), nil
}

func planDebianPreRemove(name string, args []string) ([]command, error) {
	switch {
	case len(args) == 1 && args[0] == "remove":
		return finalRemoveCommands(), nil
	case len(args) == 2 && args[0] == "upgrade" && args[1] != "":
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: %s expects remove or upgrade new-version", errMalformedInvocation, name)
	}
}

func planRPM(name string, args []string) ([]command, error) {
	if len(args) != 2 || args[0] == "" {
		return nil, fmt.Errorf("%w: %s expects scriptlet-path count", errMalformedInvocation, name)
	}
	count, err := strconv.Atoi(args[1])
	if err != nil {
		return nil, fmt.Errorf("parse RPM scriptlet count %q: %w", args[1], err)
	}
	if count < 0 {
		return nil, fmt.Errorf("%w: RPM scriptlet count must not be negative", errMalformedInvocation)
	}

	switch name {
	case "gobfd-postinstall":
		if count == 0 {
			return nil, fmt.Errorf("%w: RPM post-install count must be at least one", errMalformedInvocation)
		}
		return installCommands(), nil
	case "gobfd-preremove":
		if count == 0 {
			return finalRemoveCommands(), nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unknown RPM executable %q", errMalformedInvocation, name)
	}
}

func installCommands() []command {
	return []command{
		{name: "systemd-sysusers", args: []string{sysusersConfig}},
		{name: "systemd-tmpfiles", args: []string{"--create", tmpfilesConfig}},
		{name: "systemctl", args: []string{"daemon-reload"}, optional: true},
	}
}

func finalRemoveCommands() []command {
	return []command{
		{name: "systemctl", args: []string{"stop", serviceUnit}, optional: true, tolerateFailure: true},
		{name: "systemctl", args: []string{"disable", serviceUnit}, optional: true, tolerateFailure: true},
		{name: "systemctl", args: []string{"daemon-reload"}, optional: true},
	}
}

func execute(ctx context.Context, commands []command, logger *slog.Logger) error {
	for _, command := range commands {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("before command %s: %w", command.name, err)
		}

		path, err := exec.LookPath(command.name)
		if err != nil {
			if command.optional && errors.Is(err, exec.ErrNotFound) {
				logger.Debug("optional lifecycle command is unavailable", "command", command.name)
				continue
			}
			return fmt.Errorf("locate command %s: %w", command.name, err)
		}

		cmd := exec.CommandContext(ctx, path, command.args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err == nil {
			continue
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("run command %s: %w", command.name, contextErr)
		}
		if command.tolerateFailure {
			logger.Warn("lifecycle command failed; continuing",
				"command", command.name,
				"arguments", command.args,
				"error", err,
			)
			continue
		}
		return fmt.Errorf("run command %s: %w", command.name, err)
	}
	return nil
}
