package clabbootstrap

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLifecycleReceiptRejectsAmbiguousOrMalformedJSON(t *testing.T) {
	for _, kind := range []string{"duplicate-member", "invalid-utf8", "trailing-document"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := ensureRuntimeDirectory(root); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(lifecycleReceipt{
				SchemaVersion: 1, RunID: "json-run",
				Topology: runtimeTopologyPath(root), Containers: map[string]string{gobfdContainer: "owned-id"},
			})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "duplicate-member":
				data = append([]byte(`{"schema_version":1,`), data[1:]...)
			case "invalid-utf8":
				data = []byte(strings.Replace(string(data), "owned-id", string([]byte{0xff}), 1))
			case "trailing-document":
				data = append(data, []byte("\n{}")...)
			}
			if err := os.WriteFile(receiptPath(root), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadLifecycleReceipt(root); err == nil {
				t.Fatalf("ambiguous ownership receipt %s was accepted", kind)
			}
		})
	}
}
