package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// Sentinel errors for CLI validation.
var (
	errPeerRequired       = errors.New("--peer flag is required")
	errUnknownSessionType = errors.New("unknown session type, expected single-hop or multi-hop")
)

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage BFD sessions",
	}

	cmd.AddCommand(sessionListCmd())
	cmd.AddCommand(sessionShowCmd())
	cmd.AddCommand(sessionAddCmd())
	cmd.AddCommand(sessionDeleteCmd())

	return cmd
}

// --- session list ---

func sessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all BFD sessions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			resp, err := client.ListSessions(context.Background(), &bfdv1.ListSessionsRequest{})
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}

			out, err := formatSessions(resp.GetSessions(), outputFormat)
			if err != nil {
				return fmt.Errorf("format sessions: %w", err)
			}

			fmt.Print(out)

			return nil
		},
	}
}

// --- session show ---

func sessionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <peer-address-or-discriminator>",
		Short: "Show details of a BFD session",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			req := buildGetSessionRequest(args[0])

			resp, err := client.GetSession(context.Background(), req)
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}

			out, err := formatSession(resp.GetSession(), outputFormat)
			if err != nil {
				return fmt.Errorf("format session: %w", err)
			}

			fmt.Print(out)

			return nil
		},
	}
}

// buildGetSessionRequest parses the identifier argument as either a uint32
// discriminator or a peer IP address string.
func buildGetSessionRequest(identifier string) *bfdv1.GetSessionRequest {
	// Try parsing as a numeric discriminator first (uint32 range).
	discr, err := strconv.ParseUint(identifier, 10, 32)
	if err == nil {
		return &bfdv1.GetSessionRequest{
			Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{
				LocalDiscriminator: uint32(discr),
			},
		}
	}

	// Fall back to treating it as a peer address.
	return &bfdv1.GetSessionRequest{
		Identifier: &bfdv1.GetSessionRequest_PeerAddress{
			PeerAddress: identifier,
		},
	}
}

// --- session add ---

func sessionAddCmd() *cobra.Command {
	var (
		peer       string
		local      string
		iface      string
		sessType   string
		txInterval time.Duration
		rxInterval time.Duration
		detectMult uint32
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a new BFD session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if peer == "" {
				return errPeerRequired
			}

			st, err := parseSessionType(sessType)
			if err != nil {
				return fmt.Errorf("parse session type: %w", err)
			}

			req := &bfdv1.AddSessionRequest{
				PeerAddress:           peer,
				LocalAddress:          local,
				InterfaceName:         iface,
				Type:                  st,
				DesiredMinTxInterval:  durationpb.New(txInterval),
				RequiredMinRxInterval: durationpb.New(rxInterval),
				DetectMultiplier:      detectMult,
			}

			resp, err := client.AddSession(context.Background(), req)
			if err != nil {
				return fmt.Errorf("add session: %w", err)
			}

			out, err := formatSession(resp.GetSession(), outputFormat)
			if err != nil {
				return fmt.Errorf("format session: %w", err)
			}

			fmt.Print(out)

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&peer, "peer", "", "peer IP address (required)")
	flags.StringVar(&local, "local", "", "local IP address")
	flags.StringVar(&iface, "interface", "", "network interface name")
	flags.StringVar(&sessType, "type", "single-hop", "session type: single-hop or multi-hop")
	flags.DurationVar(&txInterval, "tx-interval", time.Second, "desired minimum TX interval")
	flags.DurationVar(&rxInterval, "rx-interval", time.Second, "required minimum RX interval")
	flags.Uint32Var(&detectMult, "detect-mult", 3, "detection multiplier (RFC 5880 Section 6.1)")

	return cmd
}

// parseSessionType converts a CLI string to the protobuf SessionType enum.
func parseSessionType(s string) (bfdv1.SessionType, error) {
	switch s {
	case "single-hop":
		return bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, nil
	case "multi-hop":
		return bfdv1.SessionType_SESSION_TYPE_MULTI_HOP, nil
	default:
		return bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED,
			fmt.Errorf("%w: %q", errUnknownSessionType, s)
	}
}

// --- session delete ---

func sessionDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <discriminator>",
		Short: "Delete a BFD session by local discriminator",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			discr, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return fmt.Errorf("parse discriminator %q: %w", args[0], err)
			}

			_, err = client.DeleteSession(context.Background(), &bfdv1.DeleteSessionRequest{
				LocalDiscriminator: uint32(discr),
			})
			if err != nil {
				return fmt.Errorf("delete session: %w", err)
			}

			fmt.Printf("Session %d deleted.\n", discr)

			return nil
		},
	}
}
