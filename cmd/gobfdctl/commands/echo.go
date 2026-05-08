package commands

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// Sentinel errors for echo CLI validation.
var (
	errEchoPeerRequired       = errors.New("--peer is required")
	errEchoTxIntervalNonPos   = errors.New("--tx-interval must be > 0")
	errEchoDetectMultRequired = errors.New("--detect-mult must be >= 1")
)

func echoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "echo",
		Short: "Manage RFC 9747 unaffiliated BFD echo sessions",
	}
	cmd.AddCommand(echoListCmd())
	cmd.AddCommand(echoAddCmd())
	cmd.AddCommand(echoDeleteCmd())
	return cmd
}

func echoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all echo sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := echoClient.ListEchoSessions(cmd.Context(), &bfdv1.ListEchoSessionsRequest{})
			if err != nil {
				return fmt.Errorf("list echo sessions: %w", err)
			}

			out, err := formatEchoSessions(resp.GetSessions(), outputFormat)
			if err != nil {
				return fmt.Errorf("format echo sessions: %w", err)
			}
			fmt.Print(out)
			return nil
		},
	}
}

type addEchoOptions struct {
	peer       string
	local      string
	iface      string
	txInterval time.Duration
	detectMult uint32
}

func echoAddCmd() *cobra.Command {
	opts := addEchoOptions{
		txInterval: time.Second,
		detectMult: 3,
	}

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a new echo session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req, err := buildAddEchoSessionRequest(opts)
			if err != nil {
				return err
			}
			resp, err := echoClient.AddEchoSession(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("add echo session: %w", err)
			}
			fmt.Printf("Echo session created: discriminator=%d state=%s\n",
				resp.GetSession().GetLocalDiscriminator(),
				shortState(resp.GetSession().GetLocalState()))
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.peer, "peer", "", "echo target IP address (required)")
	flags.StringVar(&opts.local, "local", "", "local IP address")
	flags.StringVar(&opts.iface, "interface", "", "outbound interface name")
	flags.DurationVar(&opts.txInterval, "tx-interval", opts.txInterval, "echo transmit interval")
	flags.Uint32Var(&opts.detectMult, "detect-mult", opts.detectMult, "detection multiplier (RFC 9747)")
	return cmd
}

func echoDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <discriminator>",
		Short: "Delete an echo session by local discriminator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			discr, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return fmt.Errorf("parse discriminator %q: %w", args[0], err)
			}
			_, err = echoClient.DeleteEchoSession(cmd.Context(), &bfdv1.DeleteEchoSessionRequest{
				LocalDiscriminator: uint32(discr),
			})
			if err != nil {
				return fmt.Errorf("delete echo session: %w", err)
			}
			fmt.Printf("Echo session %d deleted.\n", discr)
			return nil
		},
	}
}

func buildAddEchoSessionRequest(opts addEchoOptions) (*bfdv1.AddEchoSessionRequest, error) {
	if opts.peer == "" {
		return nil, errEchoPeerRequired
	}
	if opts.txInterval <= 0 {
		return nil, errEchoTxIntervalNonPos
	}
	if opts.detectMult == 0 {
		return nil, errEchoDetectMultRequired
	}
	return &bfdv1.AddEchoSessionRequest{
		PeerAddress:      opts.peer,
		LocalAddress:     opts.local,
		InterfaceName:    opts.iface,
		TxInterval:       durationpb.New(opts.txInterval),
		DetectMultiplier: opts.detectMult,
	}, nil
}
