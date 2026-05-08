package appversion

import "fmt"

// Version is the semantic version (e.g., "v0.1.0" or "dev").
var Version = "dev"

// GitCommit is the short git commit hash at build time.
var GitCommit = "unknown"

// BuildDate is the RFC 3339 build timestamp.
var BuildDate = "unknown"

// Full returns a human-readable multi-line version string.
func Full(binary string) string {
	return fmt.Sprintf("%s %s\n  commit:  %s\n  built:   %s", binary, Version, GitCommit, BuildDate)
}
