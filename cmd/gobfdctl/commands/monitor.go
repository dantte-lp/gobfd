package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
)

func monitorCmd() *cobra.Command {
	var includeCurrent bool

	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Stream BFD session events",
		Long:  "Connects to the gobfd daemon and streams session events until interrupted (Ctrl+C).",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			stream, err := client.WatchSessionEvents(ctx, &bfdv1.WatchSessionEventsRequest{
				IncludeCurrent: includeCurrent,
			})
			if err != nil {
				return fmt.Errorf("watch session events: %w", err)
			}
			defer stream.Close()

			for {
				msg2, err := stream.Receive()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return fmt.Errorf("stream error: %w", err)
				}
				msg := msg2

				out, fmtErr := formatEvent(msg, outputFormat)
				if fmtErr != nil {
					return fmt.Errorf("format event: %w", fmtErr)
				}

				fmt.Println(out)
			}

			// Context cancellation (Ctrl+C) is expected, not an error.

			return nil
		},
	}

	cmd.Flags().BoolVar(&includeCurrent, "current", false,
		"include current sessions before streaming changes")

	return cmd
}
