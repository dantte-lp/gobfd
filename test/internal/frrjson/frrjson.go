// Package frrjson extracts JSON payloads from FRR vtysh output.
package frrjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var errNoJSONArray = errors.New("no JSON array in FRR vtysh output")

// ExtractJSONArray returns the first complete JSON array from output.
func ExtractJSONArray(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	for start := strings.Index(trimmed, "["); start >= 0; {
		decoder := json.NewDecoder(strings.NewReader(trimmed[start:]))
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err == nil && strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
			return string(raw), nil
		}

		next := strings.Index(trimmed[start+1:], "[")
		if next < 0 {
			break
		}
		start += next + 1
	}
	return "", fmt.Errorf("%w: %s", errNoJSONArray, output)
}
