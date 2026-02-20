// Package appversion provides build version information injected via ldflags.
package appversion

// Version is set at build time via:
//
//	-ldflags="-X github.com/dantte-lp/gobfd/internal/version.Version=v1.0.0"
var Version = "dev"
