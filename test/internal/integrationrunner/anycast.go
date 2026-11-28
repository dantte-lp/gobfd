package integrationrunner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const (
	anycastProject      = "gobfd-exabgp-anycast"
	anycastPrefix       = "198.51.100.1/32"
	anycastAnnounceWait = 60 * time.Second
	anycastWithdrawWait = 30 * time.Second
	anycastPollInterval = time.Second
)

var errRouteState = errors.New("anycast route did not reach the expected state")

// exabgpAnycastLifecycle runs the anycast scenario inside the capped,
// ownership-checked interop project lifecycle: sequential bounded builds,
// start without implicit builds, and exact cleanup.
func (r *runner) exabgpAnycastLifecycle(ctx context.Context) error {
	controller, err := interopproject.NewProject(r.root, anycastProject, "exabgp-anycast", r.stdout, r.stderr)
	if err != nil {
		return fmt.Errorf("create anycast project controller: %w", err)
	}
	if err := controller.Lifecycle(ctx, r.exabgpAnycast); err != nil {
		return fmt.Errorf("exabgp-anycast lifecycle: %w", err)
	}
	return nil
}

func (r *runner) exabgpAnycast(ctx context.Context, controller *interopproject.Controller) error {
	gobgpID, err := controller.ContainerID(ctx, "gobgp-anycast")
	if err != nil {
		return fmt.Errorf("resolve GoBGP container: %w", err)
	}
	// Pausing the receiver-side GoBFD fails the BFD path as seen from the
	// anycast node: its GoBFD detects Down and the bridge withdraws the route.
	// Pausing the anycast node GoBFD would also freeze the bridge event source.
	receiverID, err := controller.ContainerID(ctx, "gobfd-receiver-anycast")
	if err != nil {
		return fmt.Errorf("resolve receiver GoBFD container: %w", err)
	}
	exabgpID, err := controller.ContainerID(ctx, "exabgp-anycast")
	if err != nil {
		return fmt.Errorf("resolve ExaBGP container: %w", err)
	}
	tsharkID, err := controller.ContainerID(ctx, "tshark-anycast")
	if err != nil {
		return fmt.Errorf("resolve tshark container: %w", err)
	}
	routePresent := func(ctx context.Context) (bool, error) {
		output, queryErr := r.output(ctx, commandTimeout, "podman", "exec", gobgpID, "gobgp", "global", "rib")
		return strings.Contains(output, anycastPrefix), queryErr
	}

	r.info("Waiting for BGP route %s", anycastPrefix)
	if err := waitForRoute(ctx, routePresent, true, anycastAnnounceWait); err != nil {
		r.bestEffort(ctx, "podman", "exec", gobgpID, "gobgp", "neighbor")
		return err
	}
	r.info("Route %s announced", anycastPrefix)
	r.capture(ctx, tsharkID, 5)

	r.info("Pausing receiver GoBFD to fail the BFD path")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "pause", receiverID); err != nil {
		return err
	}
	withdrawErr := waitForRoute(ctx, routePresent, false, anycastWithdrawWait)
	r.info("Unpausing receiver GoBFD")
	if err := r.command(ctx, commandTimeout, r.stdout, r.stderr, "podman", "unpause", receiverID); err != nil {
		return errors.Join(withdrawErr, err)
	}
	if withdrawErr != nil {
		r.bestEffort(ctx, "podman", "logs", "--tail", "40", exabgpID)
		return withdrawErr
	}
	r.info("Route withdrawn after BFD failure")

	if err := waitForRoute(ctx, routePresent, true, anycastAnnounceWait); err != nil {
		return err
	}
	r.info("Route restored after BFD recovery")
	r.capture(ctx, tsharkID, 0)
	return nil
}

// waitForRoute polls query every anycastPollInterval until the route presence
// equals want or timeout elapses. Query errors are retried until the deadline.
func waitForRoute(
	ctx context.Context,
	query func(context.Context) (bool, error),
	want bool,
	timeout time.Duration,
) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		present, err := query(deadlineCtx)
		if err == nil && present == want {
			return nil
		}
		lastErr = err
		if sleepErr := sleep(deadlineCtx, anycastPollInterval); sleepErr != nil {
			return fmt.Errorf("%w: want present=%t within %s: %w", errRouteState, want, timeout,
				errors.Join(lastErr, sleepErr))
		}
	}
}
