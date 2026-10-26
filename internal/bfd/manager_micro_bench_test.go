package bfd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

type benchMicroActuator struct{}

func (benchMicroActuator) HandleMicroBFDMemberEvent(context.Context, MicroBFDMemberEvent) error {
	return nil
}

// BenchmarkMicroBFDDispatch measures RFC 7130 member state dispatch through
// the Manager: group lookup, aggregate update, fan-out and actuator handoff.
// Sixty-four groups make a linear member scan visible.
func BenchmarkMicroBFDDispatch(b *testing.B) {
	for _, tc := range []struct {
		name     string
		actuator MicroBFDActuator
	}{
		{name: "nil-actuator"},
		{name: "noop-actuator", actuator: benchMicroActuator{}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			step := newMicroDispatchStep(b, slog.DiscardHandler, tc.actuator)
			b.ReportAllocs()
			for b.Loop() {
				step()
			}
		})
	}
}

func BenchmarkMicroBFDDispatchJSON(b *testing.B) {
	//nolint:sloglint // Allocation regression requires enabled JSON encoding; DiscardHandler disables that path.
	step := newMicroDispatchStep(b, slog.NewJSONHandler(io.Discard, nil), nil)
	for range notifyChSize + 2 {
		step()
	}
	// Measure without -race, which may discard the JSON handler's pooled buffers.
	if allocs := testing.AllocsPerRun(100, step); allocs != 0 {
		b.Fatalf("enabled JSON dispatch allocations=%g, want 0", allocs)
	}
	b.ReportAllocs()
	for b.Loop() {
		step()
	}
}

func TestMicroBFDDispatchAllocations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		step := newMicroDispatchStep(t, slog.DiscardHandler, nil)
		synctest.Wait()
		if allocs := testing.AllocsPerRun(200000, step); allocs != 0 {
			t.Fatalf("Micro-BFD state dispatch allocations=%g, want 0", allocs)
		}
	})
}

func TestMicroBFDDispatchLogFields(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		step := newMicroDispatchStep(t, slog.NewJSONHandler(&buf, nil), nil)
		synctest.Wait()
		buf.Reset()
		const steps = notifyChSize + 2
		for range steps {
			step()
		}
		counts := make(map[string]int)
		for line := range bytes.SplitSeq(bytes.TrimSpace(buf.Bytes()), []byte{'\n'}) {
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			message, ok := record["msg"].(string)
			if !ok {
				t.Fatal("log record has no message")
			}
			want := map[string]any{"level": "INFO"}
			switch message {
			case "member link state changed":
				want["member"], want["min_active"] = "eth63a", float64(1)
				want["old_state"], want["new_state"], want["up_count"] = "Up", "Down", float64(0)
				if counts[message]%2 == 0 {
					want["old_state"], want["new_state"], want["up_count"] = "Down", "Up", float64(1)
				}
			case "LAG aggregate state: Up":
				want["up_count"] = float64(1)
			case "LAG aggregate state: Down":
				want["level"], want["up_count"], want["min_active"] = "WARN", float64(0), float64(1)
			case "micro-BFD aggregate state changed":
				up := counts[message]%2 == 0
				want["lag"], want["aggregate_up"], want["up_count"] = "bond63", up, float64(0)
				if up {
					want["up_count"] = float64(1)
				}
			case "public notification channel full, dropping state change":
				want["level"] = "WARN"
				if discr, valid := record["local_discr"].(float64); !valid || discr == 0 ||
					(record["new_state"] != "Up" && record["new_state"] != "Down") {
					t.Fatal("overflow log lost session identity")
				}
			default:
				t.Fatalf("unexpected dispatch log %q", message)
			}
			for key, value := range want {
				if record[key] != value {
					t.Fatalf("%s field %s=%v, want %v", message, key, record[key], value)
				}
			}
			counts[message]++
		}
		for message, count := range map[string]int{
			"member link state changed": steps, "micro-BFD aggregate state changed": steps,
			"LAG aggregate state: Up": steps / 2, "LAG aggregate state: Down": steps / 2,
			"public notification channel full, dropping state change": 2,
		} {
			if counts[message] != count {
				t.Fatalf("%s records=%d, want %d", message, counts[message], count)
			}
		}
	})
}

func newMicroDispatchStep(tb testing.TB, handler slog.Handler, actuator MicroBFDActuator) func() {
	tb.Helper()
	mgr := NewManager(slog.New(handler), WithMicroBFDActuator(actuator))
	tb.Cleanup(mgr.Close)
	const groups = 64
	for i := range groups {
		cfg := MicroBFDConfig{
			LAGInterface:          fmt.Sprintf("bond%d", i),
			MemberLinks:           []string{fmt.Sprintf("eth%da", i), fmt.Sprintf("eth%db", i)},
			PeerAddr:              netip.MustParseAddr("192.0.2.1"),
			LocalAddr:             netip.MustParseAddr("192.0.2.2"),
			DesiredMinTxInterval:  time.Second,
			RequiredMinRxInterval: time.Second,
			DetectMultiplier:      3,
			MinActiveLinks:        1,
		}
		if _, err := mgr.CreateMicroBFDGroup(cfg); err != nil {
			tb.Fatal(err)
		}
	}
	sess, err := mgr.CreateSession(context.Background(), SessionConfig{
		PeerAddr:              netip.MustParseAddr("192.0.2.1"),
		LocalAddr:             netip.MustParseAddr("192.0.2.2"),
		Interface:             fmt.Sprintf("eth%da", groups-1),
		Type:                  SessionTypeMicroBFD,
		Role:                  RolePassive,
		DesiredMinTxInterval:  time.Second,
		RequiredMinRxInterval: time.Second,
		DetectMultiplier:      3,
	}, NonOwningSenderLeaseFactory(senderLeaseTestSender{}))
	if err != nil {
		tb.Fatal(err)
	}
	up := stateChangeOf(snapshotOf(sess), StateDown)
	up.NewState = StateUp
	down := up
	down.OldState, down.NewState = StateUp, StateDown
	ctx := context.Background()

	upNext := true
	return func() {
		if upNext {
			mgr.dispatchStateChange(ctx, up)
		} else {
			mgr.dispatchStateChange(ctx, down)
		}
		upNext = !upNext
	}
}

func internalMicroLeaseFactory(string) SenderLeaseFactory {
	return NonOwningSenderLeaseFactory(senderLeaseTestSender{})
}
