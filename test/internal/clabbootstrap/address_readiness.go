package clabbootstrap

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

const jsonTrue = "true"

type ipv6AddressInfo struct {
	Family    string          `json:"family"`
	Local     string          `json:"local"`
	Tentative json.RawMessage `json:"tentative"`
	DADFailed json.RawMessage `json:"dadfailed"`
}

func waitGoBFDIPv6Address(ctx context.Context, runner Runner, interfaceName, local string) error {
	address, err := netip.ParseAddr(local)
	if err != nil {
		return fmt.Errorf("parse IPv6 readiness address: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		result, runErr := runner.Run(ctx, Command{
			Executable: executablePodman,
			Arguments: []string{
				podmanExec, gobfdContainer, "ip", "-6", "-json", "address", "show",
				"dev", interfaceName, "to", local + "/128",
			},
		})
		if runErr != nil {
			return fmt.Errorf("inspect IPv6 address %s on %s: %w", local, interfaceName, runErr)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("inspect IPv6 address %s on %s: exit %d: %s: %w",
				local, interfaceName, result.ExitCode, strings.TrimSpace(result.Stderr), ErrBootstrapFailed)
		}
		ready, inspectErr := usableIPv6Address(result.Stdout, interfaceName, address)
		if inspectErr != nil {
			return fmt.Errorf("inspect IPv6 address %s on %s: %w", local, interfaceName, inspectErr)
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for usable IPv6 address %s on %s (absent or tentative): %w",
				local, interfaceName, ctx.Err())
		case <-ticker.C:
		}
	}
}

func usableIPv6Address(output, interfaceName string, want netip.Addr) (bool, error) {
	var interfaces []struct {
		Name      string            `json:"ifname"`
		Addresses []ipv6AddressInfo `json:"addr_info"`
	}
	if !jsontext.Value(output).IsValid() {
		return false, fmt.Errorf("invalid IPv6 address inventory JSON: %w", ErrBootstrapFailed)
	}
	if err := json.Unmarshal([]byte(output), &interfaces); err != nil {
		return false, fmt.Errorf("decode IPv6 address inventory: %w", err)
	}
	if interfaces == nil {
		return false, fmt.Errorf("null IPv6 address inventory: %w", ErrBootstrapFailed)
	}
	for _, device := range interfaces {
		if device.Name != interfaceName {
			continue
		}
		for _, address := range device.Addresses {
			if ready, err := address.usable(want); err != nil || ready {
				return ready, err
			}
		}
	}
	return false, nil
}

func (address ipv6AddressInfo) usable(want netip.Addr) (bool, error) {
	if address.Family != "inet6" {
		return false, nil
	}
	local, err := netip.ParseAddr(address.Local)
	if err != nil {
		return false, fmt.Errorf("parse observed IPv6 address: %w", err)
	}
	if local != want {
		return false, nil
	}
	for _, flag := range []json.RawMessage{address.Tentative, address.DADFailed} {
		switch string(flag) {
		case "", jsonTrue, "false":
		default:
			return false, fmt.Errorf("invalid IPv6 address flag: %w", ErrBootstrapFailed)
		}
	}
	if string(address.DADFailed) == jsonTrue {
		return false, fmt.Errorf("IPv6 duplicate address detection failed: %w", ErrBootstrapFailed)
	}
	return string(address.Tentative) != jsonTrue, nil
}
