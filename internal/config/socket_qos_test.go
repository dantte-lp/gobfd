package config_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestSocketQoSDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		yaml string
		want map[string]any
	}{
		{
			name: "defaults", yaml: "{}\n",
			want: map[string]any{
				"ipv4_dscp": uint64(48), "ipv6_traffic_class": uint64(192),
				"priority": uint64(0), "mark": uint64(0),
				"priority_required": true, "mark_required": true,
			},
		},
		{
			name: "explicit best effort", yaml: "socket:\n  ipv4_dscp: 0\n  ipv6_traffic_class: 0\n",
			want: map[string]any{"ipv4_dscp": uint64(0), "ipv6_traffic_class": uint64(0)},
		},
		{
			name: "overrides",
			yaml: "socket:\n  ipv4_dscp: 46\n  ipv6_traffic_class: 184\n  priority: 6\n  mark: 123\n" +
				"  priority_required: false\n  mark_required: false\n",
			want: map[string]any{
				"ipv4_dscp": uint64(46), "ipv6_traffic_class": uint64(184),
				"priority": uint64(6), "mark": uint64(123),
				"priority_required": false, "mark_required": false,
			},
		},
		{
			name: "upper bounds",
			yaml: "socket:\n  ipv4_dscp: 63\n  ipv6_traffic_class: 255\n  priority: 2147483647\n  mark: 4294967295\n",
			want: map[string]any{
				"ipv4_dscp": uint64(63), "ipv6_traffic_class": uint64(255),
				"priority": uint64(2147483647), "mark": uint64(4294967295),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := config.Load(writeTemp(t, test.yaml))
			if err != nil {
				t.Fatal(err)
			}
			assertSocketQoSFields(t, cfg, test.want)
		})
	}
}

func TestSocketQoSEnvironmentOverrides(t *testing.T) {
	for name, value := range map[string]string{
		"GOBFD_SOCKET_IPV4_DSCP": "34", "GOBFD_SOCKET_IPV6_TRAFFIC_CLASS": "136",
		"GOBFD_SOCKET_PRIORITY": "5", "GOBFD_SOCKET_MARK": "42",
		"GOBFD_SOCKET_PRIORITY_REQUIRED": "false", "GOBFD_SOCKET_MARK_REQUIRED": "false",
	} {
		t.Setenv(name, value)
	}
	contents := "socket:\n  ipv4_dscp: 46\n  ipv6_traffic_class: 184\n  priority: 6\n  mark: 123\n"
	cfg, err := config.Load(writeTemp(t, contents))
	if err != nil {
		t.Fatal(err)
	}
	assertSocketQoSFields(t, cfg, map[string]any{
		"ipv4_dscp": uint64(34), "ipv6_traffic_class": uint64(136),
		"priority": uint64(5), "mark": uint64(42),
		"priority_required": false, "mark_required": false,
	})
}

func TestSocketQoSRejectsInvalidYAML(t *testing.T) {
	t.Parallel()
	for _, field := range []struct {
		name     string
		overflow string
	}{
		{"ipv4_dscp", "64"},
		{"ipv6_traffic_class", "256"},
		{"priority", "2147483648"},
		{"mark", "4294967296"},
	} {
		for index, scalar := range []string{
			field.overflow, "-1", "0.5", "true", ".nan", ".inf",
			`"-1"`, `"0.5"`, "18446744073709551616",
		} {
			t.Run(fmt.Sprintf("%s/case%d", field.name, index), func(t *testing.T) {
				t.Parallel()
				_, err := config.Load(writeTemp(t, fmt.Sprintf("socket:\n  %s: %s\n", field.name, scalar)))
				if err == nil {
					t.Fatalf("socket.%s accepted invalid QoS scalar %s", field.name, scalar)
				}
				if !strings.Contains(err.Error(), "socket."+field.name) {
					t.Fatalf("error does not identify socket.%s: %v", field.name, err)
				}
			})
		}
	}
}

func TestSocketQoSRejectsNullParent(t *testing.T) {
	for _, withMark := range []bool{false, true} {
		t.Run(fmt.Sprintf("mark_env=%t", withMark), func(t *testing.T) {
			if withMark {
				t.Setenv("GOBFD_SOCKET_MARK", "123")
			}
			cfg, err := config.Load(writeTemp(t, "socket: null\n"))
			if err == nil {
				t.Fatalf("socket:null accepted with mark=%d mark_required=%t priority_required=%t",
					cfg.Socket.Mark, cfg.Socket.MarkRequired, cfg.Socket.PriorityRequired)
			}
			if !strings.Contains(err.Error(), "socket") {
				t.Fatalf("error does not identify the malformed socket policy: %v", err)
			}
		})
	}
}

func TestSocketQoSRejectsInvalidFailurePolicy(t *testing.T) {
	t.Parallel()
	for _, field := range []struct {
		name string
		err  error
	}{
		{"priority_required", config.ErrInvalidSocketPriorityRequired},
		{"mark_required", config.ErrInvalidSocketMarkRequired},
	} {
		for index, scalar := range []string{"null", "invalid", "1", "[true]", "{}"} {
			t.Run(fmt.Sprintf("%s/case%d", field.name, index), func(t *testing.T) {
				t.Parallel()
				_, err := config.Load(writeTemp(t, fmt.Sprintf("socket:\n  %s: %s\n", field.name, scalar)))
				if !errors.Is(err, field.err) {
					t.Fatalf("invalid socket.%s failure policy: error=%v want=%v", field.name, err, field.err)
				}
			})
		}
	}
}

func assertSocketQoSFields(t *testing.T, cfg *config.Config, want map[string]any) {
	t.Helper()
	actual := map[string]any{
		"ipv4_dscp": uint64(cfg.Socket.IPv4DSCP), "ipv6_traffic_class": uint64(cfg.Socket.IPv6TrafficClass),
		"priority": uint64(cfg.Socket.Priority), "mark": uint64(cfg.Socket.Mark),
		"priority_required": cfg.Socket.PriorityRequired, "mark_required": cfg.Socket.MarkRequired,
	}
	for key, expected := range want {
		if actual[key] != expected {
			t.Errorf("socket.%s = %v, want %v", key, actual[key], expected)
		}
	}
}
