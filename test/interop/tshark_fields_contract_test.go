//go:build interop || interop_testcontainers

package interop_test

import (
	"reflect"
	"testing"
)

func TestTsharkFilteredRowLimit(t *testing.T) {
	t.Parallel()
	output := "\n511\tbefore\n\n531\tafter\n554\ttail\n"
	for _, test := range []struct {
		name  string
		limit int
		want  [][]string
	}{
		{"first matching row", 1, [][]string{{"511", "before"}}},
		{"two matching rows", 2, [][]string{{"511", "before"}, {"531", "after"}}},
		{"unlimited", 0, [][]string{{"511", "before"}, {"531", "after"}, {"554", "tail"}}},
		{"short capture", 4, [][]string{{"511", "before"}, {"531", "after"}, {"554", "tail"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if rows := parseTsharkFieldRows(output, test.limit); !reflect.DeepEqual(rows, test.want) {
				t.Fatalf("filtered rows=%v, want %v", rows, test.want)
			}
		})
	}
	if rows := parseTsharkFieldRows("\n", 1); rows != nil {
		t.Fatalf("empty capture rows=%v", rows)
	}
}
