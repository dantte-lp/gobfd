// Package version provides build version information injected via ldflags.
package version

// Version is set at build time via:
//
//	-ldflags="-X github.com/wolfguard/gobfd/internal/version.Version=v1.0.0"
var Version = "dev"
