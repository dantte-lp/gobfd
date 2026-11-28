package strictjson

import (
	"errors"
	"testing"
)

func TestValidateRejectsAmbiguousAndMalformedDocuments(t *testing.T) {
	t.Parallel()

	for _, data := range []string{
		`{"private":false,"private":true}`,
		`{"dependencies":{"release-please":"1.0.0","release-please":"2.0.0"}}`,
		`[{"private":false,"\u0070rivate":true}]`,
		`{"items":[{"a":1,"a":2}]}`,
		`{} {}`, `{}null`, `true false`, `[] trailing`,
		``, `{"unfinished":`, `[{]`, "{\"text\":\"\xff\"}",
	} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			if err := Validate([]byte(data)); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("Validate() error = %v, want invalid document", err)
			}
		})
	}
}

func TestValidatePreservesDistinctKeysAndJSONValues(t *testing.T) {
	t.Parallel()

	for _, data := range []string{
		`{"private":false,"Private":true}`,
		`{"a":1,"nested":{"a":2},"array":[{"a":3},{"a":4}]}`,
		`[null,true,false,1e100000,"\u0070rivate"]`,
		`null`, `"text"`, "\n{}\t",
	} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			if err := Validate([]byte(data)); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}
