package clabbootstrap

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

type addressReadinessRunner struct {
	recordingRunner

	responses []Result
	probeErr  error
	probes    int
	started   bool
	usable    bool
}

func (runner *addressReadinessRunner) Run(ctx context.Context, command Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if slices.Contains(command.Arguments, "-json") {
		runner.probes++
		if runner.probeErr != nil {
			return Result{}, runner.probeErr
		}
		result := runner.responses[0]
		if len(runner.responses) > 1 {
			runner.responses = runner.responses[1:]
		}
		runner.usable = result.Stdout == readyIPv6Inventory
		return result, nil
	}
	if slices.Contains(command.Arguments, "/bin/gobfd") {
		runner.started = true
		if !runner.usable && !command.DryRun {
			return Result{}, errors.New("IPv6 bind before DAD completion")
		}
	}
	return runner.recordingRunner.Run(ctx, command)
}

const readyIPv6Inventory = `[{"ifname":"eth6","addr_info":[{"family":"inet6","local":"fd00:0:6::"},{}]}]`

func TestConfigureGoBFDWaitsForUsableIPv6Address(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		responses []Result
		probeErr  error
		wantErr   bool
		dryRun    bool
	}{
		{name: "tentative then ready", responses: []Result{
			{Stdout: `[{"ifname":"eth6","addr_info":[{"family":"inet6","local":"fd00:0:6::","tentative":true}]}]`},
			{Stdout: readyIPv6Inventory},
		}},
		{name: "ready", responses: []Result{{Stdout: readyIPv6Inventory}}},
		{name: "missing", responses: []Result{{Stdout: `[]`}}, wantErr: true},
		{
			name: "other interface", wantErr: true,
			responses: []Result{{Stdout: `[{"ifname":"eth7","addr_info":[{"family":"inet6","local":"fd00:0:6::"}]}]`}},
		},
		{
			name: "other address", wantErr: true,
			responses: []Result{{Stdout: `[{"ifname":"eth6","addr_info":[{"family":"inet6","local":"fd00:0:6::1"}]}]`}},
		},
		{
			name: "duplicate address", wantErr: true,
			responses: []Result{{Stdout: `[{"ifname":"eth6","addr_info":[
				{"family":"inet6","local":"fd00:0:6::","dadfailed":true}]}]`}},
		},
		{name: "malformed", responses: []Result{{Stdout: `[{`}}, wantErr: true},
		{
			name: "duplicate member", wantErr: true,
			responses: []Result{{Stdout: `[{"ifname":"eth6","addr_info":[
				{"family":"inet6","local":"fd00:0:6::","tentative":true,"tentative":false}]}]`}},
		},
		{
			name: "invalid UTF8", wantErr: true,
			responses: []Result{{Stdout: "[{\"ifname\":\"eth6\",\"ignored\":\"\xff\",\"addr_info\":[]}]"}},
		},
		{
			name: "null flag", wantErr: true,
			responses: []Result{{Stdout: `[{"ifname":"eth6","addr_info":[
				{"family":"inet6","local":"fd00:0:6::","tentative":null}]}]`}},
		},
		{
			name: "wrong flag type", wantErr: true,
			responses: []Result{{Stdout: `[{"ifname":"eth6","addr_info":[
				{"family":"inet6","local":"fd00:0:6::","tentative":"false"}]}]`}},
		},
		{name: "command failure", responses: []Result{{ExitCode: 125}}, wantErr: true},
		{name: "transport failure", probeErr: context.Canceled, wantErr: true},
		{name: "dry run", dryRun: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := DefaultOptions(t.TempDir())
			options.DryRun = test.dryRun
			runner := &addressReadinessRunner{responses: test.responses, probeErr: test.probeErr}
			profile := vendorProfile{
				name: "frr", index: 6, localIPv4: "10.0.6.1", peerIPv4: "10.0.6.2",
				localIPv6: "fd00:0:6::", peerIPv6: "fd00:0:6::1", asn: 65007,
			}
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				err := configureGoBFD(ctx, options, runner, []vendorProfile{profile})
				if (err != nil) != test.wantErr || runner.started == test.wantErr {
					t.Fatalf("configure: error=%v daemonStarted=%t, wantErr=%t", err, runner.started, test.wantErr)
				}
				if !test.dryRun && runner.probes == 0 {
					t.Fatal("daemon configuration never checked address readiness")
				}
				if test.dryRun && runner.probes != 0 {
					t.Fatal("dry run probed a live address")
				}
			})
		})
	}
}
