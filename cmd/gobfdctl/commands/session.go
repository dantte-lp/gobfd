package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// Session-type literals used by both CLI parsing and human-readable output.
const (
	sessionTypeSingleHop = "single-hop"
	sessionTypeMultiHop  = "multi-hop"
)

// Sentinel errors for CLI validation.
var (
	errPeerRequired               = errors.New("--peer flag is required")
	errUnknownSessionType         = errors.New("unknown session type, expected " + sessionTypeSingleHop + " or " + sessionTypeMultiHop)
	errUnknownAuthType            = errors.New("unknown auth type")
	errAuthSecretRequired         = errors.New("--auth-secret is required when --auth-type is enabled")
	errAuthKeyMaterialWithoutType = errors.New("--auth-key-id or --auth-secret requires --auth-type")
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := client.ListSessions(cmd.Context(), &bfdv1.ListSessionsRequest{})
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
		RunE: func(cmd *cobra.Command, args []string) error {
			req := buildGetSessionRequest(args[0])

			resp, err := client.GetSession(cmd.Context(), req)
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
	opts := addSessionOptions{
		sessType:   sessionTypeSingleHop,
		txInterval: time.Second,
		rxInterval: time.Second,
		detectMult: 3,
		authType:   "none",
	}

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a new BFD session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req, err := buildAddSessionRequest(opts)
			if err != nil {
				return err
			}

			resp, err := client.AddSession(cmd.Context(), req)
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

	bindSessionAddFlags(cmd, &opts)

	return cmd
}

func bindSessionAddFlags(cmd *cobra.Command, opts *addSessionOptions) {
	flags := cmd.Flags()
	flags.StringVar(&opts.peer, "peer", "", "peer IP address (required)")
	flags.StringVar(&opts.local, "local", "", "local IP address")
	flags.StringVar(&opts.iface, "interface", "", "network interface name")
	flags.StringVar(&opts.sessType, "type", opts.sessType, "session type: "+sessionTypeSingleHop+" or "+sessionTypeMultiHop)
	flags.DurationVar(&opts.txInterval, "tx-interval", opts.txInterval, "desired minimum TX interval")
	flags.DurationVar(&opts.rxInterval, "rx-interval", opts.rxInterval, "required minimum RX interval")
	flags.Uint32Var(&opts.detectMult, "detect-mult", opts.detectMult, "detection multiplier (RFC 5880 Section 6.1)")
	flags.StringVar(&opts.authType, "auth-type", opts.authType,
		"authentication type: none, simple-password, keyed-md5, meticulous-keyed-md5, keyed-sha1, meticulous-keyed-sha1")
	flags.Uint32Var(&opts.authKeyID, "auth-key-id", opts.authKeyID, "authentication key ID (0-255)")
	flags.StringVar(&opts.authSecret, "auth-secret", "", "authentication secret")
}

type addSessionOptions struct {
	peer       string
	local      string
	iface      string
	sessType   string
	txInterval time.Duration
	rxInterval time.Duration
	detectMult uint32
	authType   string
	authKeyID  uint32
	authSecret string
}

func buildAddSessionRequest(opts addSessionOptions) (*bfdv1.AddSessionRequest, error) {
	if opts.peer == "" {
		return nil, errPeerRequired
	}

	st, err := parseSessionType(opts.sessType)
	if err != nil {
		return nil, fmt.Errorf("parse session type: %w", err)
	}

	authType, err := parseAuthType(opts.authType)
	if err != nil {
		return nil, fmt.Errorf("parse auth type: %w", err)
	}
	if authType == bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE {
		if opts.authKeyID != 0 || opts.authSecret != "" {
			return nil, errAuthKeyMaterialWithoutType
		}
	} else if opts.authSecret == "" {
		return nil, errAuthSecretRequired
	}

	return &bfdv1.AddSessionRequest{
		PeerAddress:           opts.peer,
		LocalAddress:          opts.local,
		InterfaceName:         opts.iface,
		Type:                  st,
		DesiredMinTxInterval:  durationpb.New(opts.txInterval),
		RequiredMinRxInterval: durationpb.New(opts.rxInterval),
		DetectMultiplier:      opts.detectMult,
		AuthType:              authType,
		AuthKeyId:             opts.authKeyID,
		AuthSecret:            []byte(opts.authSecret),
	}, nil
}

// parseSessionType converts a CLI string to the protobuf SessionType enum.
func parseSessionType(s string) (bfdv1.SessionType, error) {
	switch s {
	case sessionTypeSingleHop:
		return bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP, nil
	case sessionTypeMultiHop:
		return bfdv1.SessionType_SESSION_TYPE_MULTI_HOP, nil
	default:
		return bfdv1.SessionType_SESSION_TYPE_UNSPECIFIED,
			fmt.Errorf("%w: %q", errUnknownSessionType, s)
	}
}

func parseAuthType(s string) (bfdv1.AuthenticationType, error) {
	switch normalizeFlagValue(s) {
	case "", "none":
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_NONE, nil
	case "simple-password":
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_SIMPLE_PASSWORD, nil
	case "keyed-md5":
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_MD5, nil
	case "meticulous-keyed-md5":
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_MD5, nil
	case "keyed-sha1":
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_KEYED_SHA1, nil
	case "meticulous-keyed-sha1":
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_METICULOUS_KEYED_SHA1, nil
	default:
		return bfdv1.AuthenticationType_AUTHENTICATION_TYPE_UNSPECIFIED,
			fmt.Errorf("%w: %q", errUnknownAuthType, s)
	}
}

func normalizeFlagValue(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-")
}

// --- session delete ---

func sessionDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <discriminator>",
		Short: "Delete a BFD session by local discriminator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			discr, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return fmt.Errorf("parse discriminator %q: %w", args[0], err)
			}

			_, err = client.DeleteSession(cmd.Context(), &bfdv1.DeleteSessionRequest{
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
