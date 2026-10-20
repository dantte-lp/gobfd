package bfd

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestSessionConfiguredWireIntervals(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name           string
		tx, rx         time.Duration
		wantErr        error
		wireTX, wireRX uint32
	}{
		{name: "TX 1ns", tx: time.Nanosecond, rx: time.Second, wantErr: ErrInvalidWireInterval, wireRX: 1_000_000},
		{
			name: "TX 999ns", tx: time.Microsecond - time.Nanosecond, rx: time.Second,
			wantErr: ErrInvalidWireInterval, wireRX: 1_000_000,
		},
		{name: "RX 1ns", tx: time.Second, rx: time.Nanosecond, wantErr: ErrInvalidWireInterval, wireTX: 1_000_000},
		{
			name: "RX 999ns", tx: time.Second, rx: time.Microsecond - time.Nanosecond,
			wantErr: ErrInvalidWireInterval, wireTX: 1_000_000,
		},
		{name: "zero TX", rx: time.Second, wantErr: ErrInvalidTxInterval},
		{name: "negative TX", tx: -time.Nanosecond, rx: time.Second, wantErr: ErrInvalidTxInterval},
		{name: "negative RX", tx: time.Second, rx: -time.Nanosecond, wantErr: ErrInvalidRxInterval},
		{name: "TX overflow", tx: MaxWireInterval + time.Nanosecond, rx: time.Second, wantErr: ErrInvalidWireInterval},
		{name: "RX overflow", tx: time.Second, rx: MaxWireInterval + time.Nanosecond, wantErr: ErrInvalidWireInterval},
		{name: "minimum positive", tx: time.Microsecond, rx: time.Microsecond, wireTX: 1, wireRX: 1},
		{name: "explicit zero RX", tx: time.Microsecond, wireTX: 1},
		{name: "maximum", tx: MaxWireInterval, rx: MaxWireInterval, wireTX: 0xffff_ffff, wireRX: 0xffff_ffff},
		{name: "fractional microsecond", tx: 1999 * time.Nanosecond, rx: 2999 * time.Nanosecond, wireTX: 1, wireRX: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := ownershipTestConfig("192.0.2.90")
			cfg.DesiredMinTxInterval, cfg.RequiredMinRxInterval = tt.tx, tt.rx
			for name, err := range map[string]error{
				"ValidateSessionConfig":    ValidateSessionConfig(cfg),
				"ValidateReconcileConfigs": ValidateReconcileConfigs([]ReconcileConfig{{SessionConfig: cfg}}),
			} {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("%s() error = %v, want %v", name, err, tt.wantErr)
				}
			}
			sender := &retryCachedPacketSender{}
			sess, err := NewSession(cfg, 42, sender, nil, slog.New(slog.DiscardHandler))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("NewSession() error = %v, want %v", err, tt.wantErr)
			}
			if sess == nil {
				if err == nil {
					t.Fatal("NewSession() returned neither session nor error")
				}
				return
			}
			if sess.DesiredMinTxInterval() != tt.tx || sess.RequiredMinRxInterval() != tt.rx {
				t.Fatal("constructor changed configured timer precision")
			}

			// Exercise the Up advertisement: Down's mandatory TX floor hides wire zero.
			tx, detect := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
			defer tx.Stop()
			defer detect.Stop()
			peer := ownershipDownPacket(7)
			peer.State, peer.YourDiscriminator, peer.Poll = StateInit, 42, true
			sess.handleRecvPacket(t.Context(), recvItem{pkt: *peer}, tx, detect)
			if sess.State() != StateUp || len(sender.packets) == 0 {
				t.Fatalf("Init/Poll did not produce an Up advertisement: state = %s, packets = %d",
					sess.State(), len(sender.packets))
			}
			var wire ControlPacket
			if err := UnmarshalControlPacket(sender.packets[len(sender.packets)-1], &wire); err != nil {
				t.Fatal(err)
			}
			if wire.State != StateUp || wire.DesiredMinTxInterval != tt.wireTX || wire.RequiredMinRxInterval != tt.wireRX {
				t.Fatalf("Up wire state/TX/RX = %s/%d/%d, want Up/%d/%d",
					wire.State, wire.DesiredMinTxInterval, wire.RequiredMinRxInterval, tt.wireTX, tt.wireRX)
			}
			if tt.wantErr != nil {
				t.Errorf("accepted configured TX/RX = %s/%s emitted Up wire TX/RX = %d/%d; want rejection with %v",
					tt.tx, tt.rx, wire.DesiredMinTxInterval, wire.RequiredMinRxInterval, tt.wantErr)
			}
		})
	}
}
