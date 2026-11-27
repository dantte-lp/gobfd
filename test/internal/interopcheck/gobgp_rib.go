package interopcheck

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/netip"
)

const maxGoBGPRIBSize = 1 << 20

var errGoBGPRIB = errors.New("invalid GoBGP RIB observation")

type goBGPRIBPath struct {
	Best *bool `json:"best"`
}

// GoBGPRouteExists validates a complete GoBGP v4 JSON RIB before testing membership.
// The pinned CLI emits destination path arrays; best:false is still a route.
func GoBGPRouteExists(input []byte, prefix string) (bool, error) {
	wanted, err := goBGPRIBPrefix(prefix)
	if err != nil {
		return false, err
	}
	if len(input) == 0 || len(input) > maxGoBGPRIBSize {
		return false, fmt.Errorf("%w: payload size %d", errGoBGPRIB, len(input))
	}
	var destinations map[string][]*goBGPRIBPath
	if err := json.Unmarshal(input, &destinations); err != nil {
		return false, fmt.Errorf("%w: decode JSON: %w", errGoBGPRIB, err)
	}
	if destinations == nil {
		return false, fmt.Errorf("%w: null table", errGoBGPRIB)
	}
	present := false
	for key, paths := range destinations {
		route, err := goBGPRIBPrefix(key)
		if err != nil {
			return false, err
		}
		if len(paths) == 0 {
			return false, fmt.Errorf("%w: destination %s has no paths", errGoBGPRIB, key)
		}
		for _, path := range paths {
			if path == nil || path.Best == nil {
				return false, fmt.Errorf("%w: destination %s lacks a path witness", errGoBGPRIB, key)
			}
		}
		present = present || route == wanted
	}
	return present, nil
}

func goBGPRIBPrefix(text string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%w: prefix %q: %w", errGoBGPRIB, text, err)
	}
	if prefix != prefix.Masked() || prefix.String() != text {
		return netip.Prefix{}, fmt.Errorf("%w: noncanonical prefix %q", errGoBGPRIB, text)
	}
	return prefix, nil
}
