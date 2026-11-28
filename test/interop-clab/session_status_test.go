//go:build interop_clab

package interop_clab_test

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/netip"
)

func readGoBFDSessionStatus(ctx context.Context, peer vendorPeer) (*gobfdTimerStatus, error) {
	result, err := containerExecResult(ctx, gobfdContainer,
		"gobfdctl", "--addr", "localhost:50052", "--format", "json", "session", "list")
	if err != nil {
		return nil, fmt.Errorf("query GoBFD session state: %w", err)
	}
	if !jsontext.Value(result.Stdout).IsValid() {
		return nil, errors.New("GoBFD session state requires strict JSON")
	}
	var rows []gobfdTimerStatus
	if decodeErr := json.Unmarshal([]byte(result.Stdout), &rows); decodeErr != nil {
		return nil, fmt.Errorf("decode GoBFD session state: %w", decodeErr)
	}
	local, err := netip.ParseAddr(peer.localIP)
	if err != nil {
		return nil, fmt.Errorf("parse expected GoBFD local address: %w", err)
	}
	remote, err := netip.ParseAddr(peer.peerIP)
	if err != nil {
		return nil, fmt.Errorf("parse expected GoBFD peer address: %w", err)
	}
	row, err := selectGoBFDTimerStatus(rows, local, remote)
	if err != nil {
		return nil, err
	}
	if row.Type != "single-hop" {
		return nil, errors.New("GoBFD session state must describe the single-hop tuple")
	}
	for _, state := range []string{row.LocalState, row.RemoteState} {
		switch state {
		case "AdminDown", "Down", "Init", "Up":
		default:
			return nil, fmt.Errorf("invalid GoBFD session state %q", state)
		}
	}
	return row, nil
}

func gobfdBFDSessionDown(ctx context.Context, peer vendorPeer) (bool, error) {
	row, err := readGoBFDSessionStatus(ctx, peer)
	if err != nil {
		return false, err
	}
	return row.LocalState == "Down", nil
}

func frrBFDSessionUp(ctx context.Context, peer vendorPeer) (bool, error) {
	result, err := containerExecResult(ctx, peer.container, "vtysh", "-c", "show bfd peers json")
	if err != nil {
		return false, fmt.Errorf("query FRR session state: %w", err)
	}
	if !jsontext.Value(result.Stdout).IsValid() {
		return false, errors.New("FRR session state requires strict JSON")
	}
	var rows []frrTimerStatus
	if decodeErr := json.Unmarshal([]byte(result.Stdout), &rows); decodeErr != nil {
		return false, fmt.Errorf("decode FRR session state: %w", decodeErr)
	}
	local, err := netip.ParseAddr(peer.localIP)
	if err != nil {
		return false, fmt.Errorf("parse expected FRR peer address: %w", err)
	}
	remote, err := netip.ParseAddr(peer.peerIP)
	if err != nil {
		return false, fmt.Errorf("parse expected FRR local address: %w", err)
	}
	row, err := selectFRRTimerStatus(rows, local)
	if err != nil {
		return false, err
	}
	if row.Multihop == nil || *row.Multihop {
		return false, errors.New("FRR session state must describe a single-hop tuple")
	}
	if err := validateFRRLocalAddress(row.Local, remote); err != nil {
		return false, err
	}
	return row.Status == "up", nil
}
