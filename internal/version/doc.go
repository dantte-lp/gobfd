// Package appversion provides build version information injected via ldflags.
//
// All variables are set at build time:
//
//	-ldflags="-X github.com/dantte-lp/gobfd/internal/version.Version=v1.0.0
//	          -X github.com/dantte-lp/gobfd/internal/version.GitCommit=abc1234
//	          -X github.com/dantte-lp/gobfd/internal/version.BuildDate=2026-02-22T12:00:00Z"
package appversion
