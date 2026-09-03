package e2erunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const observabilityOwnerFile = ".gobfd-observability-owner"

type testReportSpec struct {
	reportPath          string
	artifactEnvironment string
	ownerEnvironment    string
	commandTimeout      time.Duration
	goArguments         []string
}

func testReportTarget(target string) (testReportSpec, bool) {
	switch target {
	case "core":
		return testReportSpec{
			reportPath:          "core",
			artifactEnvironment: "E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR",
			commandTimeout:      11 * time.Minute,
			goArguments: []string{
				"test", "-tags", "e2e_core_testcontainers", "./test/e2e/core", "-race", "-count=1",
				"-json", "-timeout", "10m", "-run", "^TestCoreDaemonTestcontainers$",
			},
		}, true
	case "bgp-fast-failover":
		return testReportSpec{
			reportPath:          "bgp-fast-failover",
			artifactEnvironment: "E2E_BGP_FAILOVER_TESTCONTAINERS_ARTIFACT_DIR",
			commandTimeout:      11 * time.Minute,
			goArguments: []string{
				"test", "-tags", "e2e_bgp_failover_testcontainers", "./test/e2e/bgp-failover", "-race",
				"-count=1", "-json", "-timeout", "10m", "-run", "^TestBGPFastFailoverTestcontainers$",
			},
		}, true
	case "haproxy-health":
		return testReportSpec{
			reportPath:          "haproxy-health",
			artifactEnvironment: "E2E_HAPROXY_TESTCONTAINERS_ARTIFACT_DIR",
			commandTimeout:      11 * time.Minute,
			goArguments: []string{
				"test", "-trimpath", "-tags", "e2e_haproxy_testcontainers", "./test/e2e/haproxy-health",
				"-race", "-count=1", "-json", "-timeout", "10m",
			},
		}, true
	case "observability":
		return testReportSpec{
			reportPath:          "observability",
			artifactEnvironment: "E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR",
			ownerEnvironment:    "E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER",
			commandTimeout:      16 * time.Minute,
			goArguments: []string{
				"test", "-trimpath", "-tags", "e2e_observability_testcontainers", "./test/e2e/observability",
				"-race", "-count=1", "-json", "-timeout", "15m",
			},
		}, true
	default:
		return testReportSpec{}, false
	}
}

func secureReportDirectory(root, reportPath string) (string, string, error) {
	parent := filepath.Join(root, "reports", "e2e", reportPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", "", fmt.Errorf("create %s report parent: %w", reportPath, err)
	}
	// #nosec G302 -- an owner-only directory requires execute permission for traversal.
	if err := os.Chmod(parent, 0o700); err != nil {
		return "", "", fmt.Errorf("secure %s report parent: %w", reportPath, err)
	}
	reportDir, err := os.MkdirTemp(parent, "run.")
	if err != nil {
		return "", "", fmt.Errorf("create exclusive %s report directory: %w", reportPath, err)
	}
	// #nosec G302 -- an owner-only directory requires execute permission for traversal.
	if err := os.Chmod(reportDir, 0o700); err != nil {
		secureErr := fmt.Errorf("secure %s report directory: %w", reportPath, err)
		if removeErr := os.Remove(reportDir); removeErr != nil {
			return "", "", errors.Join(
				secureErr,
				fmt.Errorf("remove unsecured %s report directory %s: %w", reportPath, reportDir, removeErr),
			)
		}
		return "", "", secureErr
	}
	return filepath.Base(reportDir), reportDir, nil
}

func (r *runner) runTestReport(ctx context.Context) error {
	spec, ok := testReportTarget(r.target)
	if !ok {
		return fmt.Errorf("%w: unknown report target %q", errUsage, r.target)
	}
	environment := reportCommandEnvironment(spec, r.reportDir)
	if spec.ownerEnvironment != "" {
		if err := writeObservabilityOwner(r.reportDir, filepath.Base(r.reportDir)); err != nil {
			return err
		}
	}
	return r.loggedCommandEnvironment(
		ctx, spec.commandTimeout, environment, append([]string{"go"}, spec.goArguments...)...,
	)
}

func reportCommandEnvironment(spec testReportSpec, reportDir string) []string {
	blocked := make(map[string]struct{}, len(artifactEnvironmentNames()))
	for _, name := range artifactEnvironmentNames() {
		blocked[name] = struct{}{}
	}
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, remove := blocked[name]; remove {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GOBFD_REQUIRE_PODMAN=1", spec.artifactEnvironment+"="+reportDir)
	if spec.ownerEnvironment != "" {
		environment = append(environment, spec.ownerEnvironment+"="+filepath.Base(reportDir))
	}
	return environment
}

func artifactEnvironmentNames() []string {
	return []string{
		"GOBFD_REQUIRE_PODMAN",
		"E2E_CORE_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_BGP_FAILOVER_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_HAPROXY_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_DIR",
		"E2E_OBSERVABILITY_TESTCONTAINERS_ARTIFACT_OWNER",
	}
}

func writeObservabilityOwner(reportDir, owner string) error {
	path := filepath.Join(reportDir, observabilityOwnerFile)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create observability report owner: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(fmt.Errorf("secure observability report owner: %w", err), file.Close())
	}
	_, writeErr := fmt.Fprintln(file, owner)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write observability report owner: %w", err)
	}
	return nil
}
