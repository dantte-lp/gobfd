package commands

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

// Sentinel errors for micro-BFD CLI validation.
var (
	errMicroLagRequired       = errors.New("--lag is required")
	errMicroMembersRequired   = errors.New("--members must list at least one interface")
	errMicroPeerRequired      = errors.New("--peer is required")
	errMicroDetectMultZero    = errors.New("--detect-mult must be >= 1")
	errMicroMinActiveOutRange = errors.New("--min-active must satisfy 1 <= n <= len(members)")
)

func microCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "micro",
		Short: "Manage RFC 7130 micro-BFD groups",
	}
	cmd.AddCommand(microListCmd())
	cmd.AddCommand(microAddCmd())
	cmd.AddCommand(microDeleteCmd())
	return cmd
}

func microListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all micro-BFD groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := microClient.ListMicroBFDGroups(cmd.Context(), &bfdv1.ListMicroBFDGroupsRequest{})
			if err != nil {
				return fmt.Errorf("list micro-bfd groups: %w", err)
			}
			out, err := formatMicroBFDGroups(resp.GetGroups(), outputFormat)
			if err != nil {
				return fmt.Errorf("format micro-bfd groups: %w", err)
			}
			fmt.Print(out)
			return nil
		},
	}
}

type addMicroOptions struct {
	lag        string
	members    []string
	peer       string
	local      string
	txInterval time.Duration
	rxInterval time.Duration
	detectMult uint32
	minActive  uint32
}

func microAddCmd() *cobra.Command {
	opts := addMicroOptions{
		txInterval: time.Second,
		rxInterval: time.Second,
		detectMult: 3,
		minActive:  1,
	}

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a new micro-BFD group",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req, err := buildAddMicroBFDGroupRequest(opts)
			if err != nil {
				return err
			}
			resp, err := microClient.AddMicroBFDGroup(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("add micro-bfd group: %w", err)
			}
			fmt.Printf("Micro-BFD group created: lag=%s members=%d min_active=%d\n",
				resp.GetGroup().GetLagInterface(),
				len(resp.GetGroup().GetMemberLinks()),
				resp.GetGroup().GetMinActiveLinks())
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.lag, "lag", "", "LAG interface name (required, e.g. bond0)")
	flags.StringSliceVar(&opts.members, "members", nil, "comma-separated member link names (required)")
	flags.StringVar(&opts.peer, "peer", "", "peer IP address (required)")
	flags.StringVar(&opts.local, "local", "", "local IP address")
	flags.DurationVar(&opts.txInterval, "tx-interval", opts.txInterval, "desired minimum TX interval")
	flags.DurationVar(&opts.rxInterval, "rx-interval", opts.rxInterval, "required minimum RX interval")
	flags.Uint32Var(&opts.detectMult, "detect-mult", opts.detectMult, "detection multiplier (RFC 7130)")
	flags.Uint32Var(&opts.minActive, "min-active", opts.minActive, "minimum members up for aggregate operational")
	return cmd
}

func microDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <lag-interface>",
		Short: "Delete a micro-BFD group by LAG interface",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := microClient.DeleteMicroBFDGroup(cmd.Context(), &bfdv1.DeleteMicroBFDGroupRequest{
				LagInterface: args[0],
			})
			if err != nil {
				return fmt.Errorf("delete micro-bfd group: %w", err)
			}
			fmt.Printf("Micro-BFD group %q deleted.\n", args[0])
			return nil
		},
	}
}

func buildAddMicroBFDGroupRequest(opts addMicroOptions) (*bfdv1.AddMicroBFDGroupRequest, error) {
	if opts.lag == "" {
		return nil, errMicroLagRequired
	}
	if len(opts.members) == 0 {
		return nil, errMicroMembersRequired
	}
	if opts.peer == "" {
		return nil, errMicroPeerRequired
	}
	if opts.detectMult == 0 {
		return nil, errMicroDetectMultZero
	}
	if opts.minActive < 1 || int(opts.minActive) > len(opts.members) {
		return nil, errMicroMinActiveOutRange
	}
	return &bfdv1.AddMicroBFDGroupRequest{
		LagInterface:          opts.lag,
		MemberLinks:           opts.members,
		PeerAddress:           opts.peer,
		LocalAddress:          opts.local,
		DesiredMinTxInterval:  durationpb.New(opts.txInterval),
		RequiredMinRxInterval: durationpb.New(opts.rxInterval),
		DetectMultiplier:      opts.detectMult,
		MinActiveLinks:        opts.minActive,
	}, nil
}
