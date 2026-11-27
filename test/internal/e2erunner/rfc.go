package e2erunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const rfcContainerCount = 8

func (r *runner) runRFCSuite(ctx context.Context, controller *interopproject.Controller) (runErr error) {
	defer func() {
		artifactCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandTimeout)
		defer cancel()
		runErr = errors.Join(runErr, r.collectRFCArtifacts(artifactCtx, controller))
	}()
	if waitErr := wait(ctx, 15*time.Second); waitErr != nil {
		return fmt.Errorf("wait for RFC topology: %w", waitErr)
	}
	devID, err := controller.ServiceContainerID(ctx, r.devProject, "dev")
	if err != nil {
		return fmt.Errorf("resolve RFC dev container: %w", err)
	}
	runErr = r.loggedCommand(ctx, podmanCommand, "exec", devID, "env",
		"INTEROP_RFC_COMPOSE_FILE=/app/test/interop-rfc/compose.yml",
		"INTEROP_PROJECT_NAME="+interopRFCProject,
		"go", goTestCommand, goTagsFlag, "interop_rfc", goRaceFlag,
		"-json", "-v", "-count=1", "-timeout", "300s", "./test/interop-rfc/")
	return errors.Join(runErr, passedGoTest(filepath.Join(r.reportDir, goTestJSONName)))
}

func (r *runner) collectRFCArtifacts(ctx context.Context, controller *interopproject.Controller) error {
	ids := make([]string, 0, rfcContainerCount)
	var tsharkID string
	for _, name := range []string{
		"gobfd-rfc-interop", "tshark-rfc-interop", "frr-rfc-interop",
		"gobfd-rfc9384-interop", "gobgp-rfc-interop", "frr-rfc-bgp-interop",
		"frr-rfc-unsolicited-interop", "echo-reflector-interop",
	} {
		id, err := controller.ContainerID(ctx, name)
		if err != nil {
			return fmt.Errorf("resolve required RFC container %s: %w", name, err)
		}
		ids = append(ids, id)
		if name == "tshark-rfc-interop" {
			tsharkID = id
		}
	}
	inspect := append([]string{podmanCommand, "inspect"}, ids...)
	if inspectErr := r.commandToFile(ctx, filepath.Join(r.reportDir, containersJSONName),
		filepath.Join(r.reportDir, "containers.err"), inspect...); inspectErr != nil {
		return fmt.Errorf("record RFC containers: %w", inspectErr)
	}
	logFile, err := secureFile(filepath.Join(r.reportDir, containersLogName))
	if err != nil {
		return fmt.Errorf("open RFC container log: %w", err)
	}
	var artifactErr error
	for _, id := range ids {
		if _, writeErr := fmt.Fprintf(logFile, "\n===== RFC container %s =====\n", id); writeErr != nil {
			artifactErr = errors.Join(artifactErr, writeErr)
			break
		}
		artifactErr = errors.Join(
			artifactErr, r.command(ctx, commandTimeout, logFile, logFile, podmanCommand, "logs", id),
		)
	}
	artifactErr = errors.Join(artifactErr, r.closeArtifact(logFile))
	pcap := filepath.Join(r.reportDir, "packets.pcapng")
	if copyErr := r.commandToFile(ctx, pcap, filepath.Join(r.reportDir, "packets.err"),
		podmanCommand, "exec", tsharkID, "cat", "/captures/bfd.pcapng"); copyErr != nil {
		return errors.Join(artifactErr, fmt.Errorf("copy RFC capture: %w", copyErr))
	}
	info, statErr := os.Lstat(pcap)
	if statErr != nil {
		return errors.Join(artifactErr, fmt.Errorf("stat RFC capture: %w", statErr))
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.Join(artifactErr, errUnsafePcap)
	}
	return errors.Join(artifactErr, r.commandToFile(ctx,
		filepath.Join(r.reportDir, "packets.csv"), filepath.Join(r.reportDir, "packets-csv.err"),
		podmanCommand, "exec", tsharkID,
		"tshark", "-r", "/captures/bfd.pcapng", "-Y", "bfd || udp.port == 3785", "-T", "fields",
		"-e", "frame.time_relative", "-e", "ip.src", "-e", "ip.dst", "-e", "udp.srcport", "-e", "udp.dstport",
		"-e", "bfd.sta", "-e", "bfd.diag", "-e", "bfd.desired_min_tx_interval", "-e", "bfd.required_min_rx_interval",
		"-e", "bfd.required_min_echo_interval", "-e", "bfd.my_discriminator", "-e", "bfd.your_discriminator",
		"-E", "header=y", "-E", "separator=,"))
}
