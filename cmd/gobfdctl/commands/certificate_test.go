package commands

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestCertificateRenewRequiresExplicitFlags(t *testing.T) {
	t.Parallel()

	flags := []string{"ca-url", "ca-root", "cert", "key"}
	for _, missing := range flags {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			cmd, _, err := certificateCmd().Find([]string{"renew"})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range flags {
				if name == missing {
					continue
				}
				if err := cmd.Flags().Set(name, "explicit-value"); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.ValidateRequiredFlags(); err == nil {
				t.Fatalf("missing --%s accepted", missing)
			}
		})
	}
}

func TestCertificateRenewRejectsPositionalArgs(t *testing.T) {
	t.Parallel()

	cmd, _, err := certificateCmd().Find([]string{"renew"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Args == nil || cmd.Args(cmd, []string{"unexpected"}) == nil {
		t.Fatal("certificate renew accepted a positional argument")
	}
}

func TestCertificateRenewBypassesDaemonRPC(t *testing.T) {
	t.Parallel()

	errDaemonRPC := errors.New("daemon RPC initialization ran")
	var daemonRPC bool
	root := &cobra.Command{
		Use:           "gobfdctl",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			daemonRPC = true
			return errDaemonRPC
		},
	}
	root.PersistentFlags().String("addr", "invalid-endpoint", "")
	root.PersistentFlags().String("tls-ca", "missing-management-root.pem", "")
	root.AddCommand(certificateCmd())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{
		"certificate", "renew", "--ca-url", "https://ca.invalid",
		"--ca-root", t.TempDir() + "/missing-root.pem",
		"--cert", "missing-cert.pem", "--key", "missing-key.pem",
	})
	err := root.ExecuteContext(t.Context())
	if daemonRPC || errors.Is(err, errDaemonRPC) {
		t.Fatal("certificate renew initialized the daemon RPC client")
	}
	if err == nil {
		t.Fatal("missing credential files accepted")
	}
	if out.Len() != 0 {
		t.Fatalf("failed renewal printed output: %q", out.String())
	}
}
