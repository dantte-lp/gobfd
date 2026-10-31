// Package strictjson validates UTF-8 JSON documents without duplicate members.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// ErrInvalidDocument identifies invalid or ambiguous JSON input.
var ErrInvalidDocument = errors.New("invalid JSON document")

// Validate rejects invalid UTF-8, duplicate decoded keys and trailing documents.
// Callers retain their own input-size and schema constraints.
func Validate(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8: %w", ErrInvalidDocument)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := readValue(decoder); err != nil {
		return fmt.Errorf("validate JSON structure: %w", errors.Join(err, ErrInvalidDocument))
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON data: %w", errors.Join(err, ErrInvalidDocument))
	}
	return nil
}

func readValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON value: %w", err)
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		return readObject(decoder)
	case '[':
		return readArray(decoder)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q: %w", delimiter, ErrInvalidDocument)
	}
}

func readObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read JSON object key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("JSON object key is not a string: %w", ErrInvalidDocument)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON field %q: %w", key, ErrInvalidDocument)
		}
		seen[key] = struct{}{}
		if err := readValue(decoder); err != nil {
			return err
		}
	}
	return readClosing(decoder, '}')
}

func readArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := readValue(decoder); err != nil {
			return err
		}
	}
	return readClosing(decoder, ']')
}

func readClosing(decoder *json.Decoder, expected json.Delim) error {
	closing, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON closing delimiter: %w", err)
	}
	if closing != expected {
		return fmt.Errorf("unexpected JSON closing delimiter %v: %w", closing, ErrInvalidDocument)
	}
	return nil
}
