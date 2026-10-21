package bfd

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"testing"
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
			mgr := NewManager(slog.New(slog.DiscardHandler), WithMicroBFDActuator(tc.actuator))
			defer mgr.Close()
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
					b.Fatal(err)
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
				b.Fatal(err)
			}
			up := stateChangeOf(snapshotOf(sess), StateDown)
			up.NewState = StateUp
			down := up
			down.OldState, down.NewState = StateUp, StateDown
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if i%2 == 0 {
					mgr.dispatchStateChange(ctx, up)
				} else {
					mgr.dispatchStateChange(ctx, down)
				}
			}
		})
	}
}

func internalMicroLeaseFactory(string) SenderLeaseFactory {
	return NonOwningSenderLeaseFactory(senderLeaseTestSender{})
}
