package interop_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeContractRejectsUnsafeOrMalformedHoloTopology(t *testing.T) {
	t.Parallel()

	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	composePath := filepath.Join(root, "test", "interop", "compose.yml")
	canonical, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read canonical Compose file: %v", err)
	}

	tests := map[string]struct {
		old string
		new string
	}{
		"privileged Holo": {
			old: "    container_name: holo-interop\n",
			new: "    container_name: holo-interop\n    privileged: true\n",
		},
		"host-networked Holo config": {
			old: "    container_name: holo-config-interop\n",
			new: "    container_name: holo-config-interop\n    network_mode: host\n",
		},
		"unknown Holo key": {
			old: "    container_name: holo-interop\n",
			new: "    container_name: holo-interop\n    unknown_contract_key: true\n",
		},
		"list-shaped Holo dependency": {
			old: "    depends_on:\n      holo:\n        condition: service_healthy\n",
			new: "    depends_on: [holo]\n",
		},
		"list-shaped Holo network": {
			old: "    networks:\n      bfdnet:\n\n  thoro:",
			new: "    networks: [bfdnet]\n\n  thoro:",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mutated, replaceErr := replaceExactlyOnce(canonical, test.old, test.new)
			if replaceErr != nil {
				t.Fatalf("mutate Compose fixture: %v", replaceErr)
			}
			if _, decodeErr := decodeCompose(mutated); decodeErr == nil {
				t.Fatal("unsafe or malformed Compose topology was accepted")
			}
		})
	}
}

func TestHoloConfigurationReadersRejectNoncanonicalInput(t *testing.T) {
	t.Parallel()

	t.Run("startup hierarchy", func(t *testing.T) {
		t.Parallel()

		misindentedStartup, replaceErr := replaceExactlyOnce(
			[]byte(holoStartupConfig),
			"  source-addr",
			"source-addr",
		)
		if replaceErr != nil {
			t.Fatalf("mutate Holo startup: %v", replaceErr)
		}
		if err := validateCanonicalConfig("Holo startup", misindentedStartup, holoStartupConfig); err == nil {
			t.Fatal("Holo startup reader accepted a misindented directive")
		}
	})

	t.Run("TOML exactness", func(t *testing.T) {
		t.Parallel()

		indentedHolod, replaceErr := replaceExactlyOnce([]byte(holodConfig), "user =", " user =")
		if replaceErr != nil {
			t.Fatalf("mutate Holo TOML: %v", replaceErr)
		}
		if err := validateCanonicalConfig("Holo TOML", indentedHolod, holodConfig); err == nil {
			t.Fatal("Holo TOML reader accepted noncanonical whitespace")
		}
	})
}

func replaceExactlyOnce(input []byte, old, replacement string) ([]byte, error) {
	if count := strings.Count(string(input), old); count != 1 {
		return nil, fmt.Errorf("replace %q: found %d occurrences, want 1", old, count)
	}
	return []byte(strings.Replace(string(input), old, replacement, 1)), nil
}
