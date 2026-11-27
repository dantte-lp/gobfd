// Package endpoint validates and classifies GoBGP gRPC endpoint targets shared
// by configuration validation and the runtime GoBGP client.
package endpoint

import (
	"net/url"
	"path"
	"strings"
)

// LocalUnixSocketPath extracts the clean absolute filesystem socket path from
// a gRPC "unix:" target ("unix:///abs/path" or "unix:/abs/path").
//
// It rejects non-Unix targets, "unix-abstract:" and "@" abstract sockets,
// relative paths, non-empty authorities, userinfo, queries, fragments, NUL
// bytes, dot-segment or duplicate-slash paths, directory paths ending in "/",
// and whitespace-padded strings.
func LocalUnixSocketPath(raw string) (string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw ||
		!strings.HasPrefix(raw, "unix:") || strings.ContainsRune(raw, 0) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || !validUnixURL(u) {
		return "", false
	}
	return cleanAbsUnixPath(u.Path)
}

func validUnixURL(u *url.URL) bool {
	if u.Scheme != "unix" || u.Opaque != "" || u.User != nil || u.Host != "" {
		return false
	}
	return u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == ""
}

func cleanAbsUnixPath(rawPath string) (string, bool) {
	if !path.IsAbs(rawPath) || strings.HasSuffix(rawPath, "/") || strings.ContainsRune(rawPath, 0) {
		return "", false
	}
	cleaned := path.Clean(rawPath)
	if cleaned == "/" || cleaned != rawPath {
		return "", false
	}
	return cleaned, true
}
