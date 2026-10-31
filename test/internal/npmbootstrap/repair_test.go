package npmbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These closed source-protocol fixtures are not runnable vendor code. The
// genuine upstream inputs and both module formats require separate native gates.
const minimatchBeforeFixture = "    firstPhasePreProcess(globParts) {\n" +
	"                        p2 === '..') {\n" +
	"    secondPhasePreProcess(globParts) {\n" +
	"//# sourceMappingURL=index.js.map"

const minimatchAfterFixture = "    firstPhasePreProcess(globParts) {\n" +
	"        const { braceExpandMax = 100_000 } = this.options;\n" +
	"                        p2 === '..' ||\n" +
	"                        globParts.length >= braceExpandMax) {\n" +
	"    secondPhasePreProcess(globParts) {\n" +
	"        if (globParts.length > 1000) return globParts;\n"

const globBeforeFixture = `{
  "name": "glob",
  "version": "13.0.6",
  "tshy": {"exports": {".": {"default": "./dist/commonjs/index.min.js"}}},
  "main": "./dist/commonjs/index.min.js",
  "module": "./dist/esm/index.min.js",
  "exports": {
    "./raw": {
      "import": {"default": "./dist/esm/index.js"},
      "require": {"default": "./dist/commonjs/index.js"}
    },
    ".": {
      "import": {"default": "./dist/esm/index.min.js"},
      "require": {"default": "./dist/commonjs/index.min.js"}
    }
  }
}
`

const globAfterFixture = `{
  "name": "glob",
  "version": "13.0.6",
  "tshy": {"exports": {".": {"default": "./dist/commonjs/index.min.js"}}},
  "main": "./dist/commonjs/index.js",
  "module": "./dist/esm/index.js",
  "exports": {
    "./raw": {
      "import": {"default": "./dist/esm/index.js"},
      "require": {"default": "./dist/commonjs/index.js"}
    },
    ".": {
      "import": {"default": "./dist/esm/index.js"},
      "require": {"default": "./dist/commonjs/index.js"}
    }
  }
}
`

func fixtureRepairInputs() [3]string {
	return [3]string{"commonjs\n" + minimatchBeforeFixture, "esm\n" + minimatchBeforeFixture, globBeforeFixture}
}

func fixtureRepairOutputs() [3]string {
	return [3]string{"commonjs\n" + minimatchAfterFixture, "esm\n" + minimatchAfterFixture, globAfterFixture}
}

func fixtureRepairHashes() [3]npmRepairHashes {
	var hashes [3]npmRepairHashes
	inputs, outputs := fixtureRepairInputs(), fixtureRepairOutputs()
	for index := range hashes {
		hashes[index] = npmRepairHashes{npmRepairDigest([]byte(inputs[index])), npmRepairDigest([]byte(outputs[index]))}
	}
	return hashes
}

func installFixture(ctx context.Context, directory string, contract Contract, client *http.Client) error {
	return install(ctx, directory, contract, client, fixtureRepairHashes())
}

func makeRepairedFixtureArchive(t *testing.T, headers []*tar.Header) ([]byte, int64, int) {
	t.Helper()
	inputs := fixtureRepairInputs()
	contents := make(map[string]string, len(inputs))
	for index, name := range npmRepairFileNames() {
		path := "package/" + name
		contents[path] = inputs[index]
		headers = append(headers, &tar.Header{
			Name: path, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(inputs[index])),
		})
	}
	data, expanded := makeArchive(t, headers, contents)
	return data, expanded, len(headers)
}

func TestPublicInstallRequiresQualifiedNPMBeforeDownload(t *testing.T) {
	t.Parallel()
	contract, bodies := fixture(t)
	root, requests := t.TempDir(), 0
	if err := Install(t.Context(), root, contract, fixtureClient(t, bodies, &requests)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unqualified npm archive accepted: %v", err)
	}
	if requests != 0 {
		t.Fatalf("unqualified npm source reached the network: %d", requests)
	}
	if _, err := os.Stat(filepath.Join(root, "npm-bootstrap")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unqualified source created a bootstrap installation: %v", err)
	}
}

func TestPublicInstallRejectsChangedRepairArchiveBindings(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*Artifact){
		"version": func(artifact *Artifact) { artifact.Version = "12.2.0" },
		"integrity": func(artifact *Artifact) {
			artifact.Integrity = "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, sha512.Size))
		},
		"compressed": func(artifact *Artifact) { artifact.ArchiveBytes-- },
		"expanded":   func(artifact *Artifact) { artifact.ExpandedBytes-- },
		"entries":    func(artifact *Artifact) { artifact.Entries-- },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			contract, bodies := fixture(t)
			contract.Artifacts[0] = npmRepairArtifact()
			change(&contract.Artifacts[0])
			requests := 0
			err := Install(t.Context(), t.TempDir(), contract, fixtureClient(t, bodies, &requests))
			if !errors.Is(err, ErrInvalid) || requests != 0 {
				t.Fatalf("changed npm source binding reached download: requests=%d error=%v", requests, err)
			}
		})
	}
}

func TestRepairRejectsChangedMissingDuplicateAndDerivedInputs(t *testing.T) {
	t.Parallel()
	inputs, outputs := fixtureRepairInputs(), fixtureRepairOutputs()
	for index, name := range npmRepairFileNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hashes := fixtureRepairHashes()[index]
			got, err := repairNPMEntry(index, []byte(inputs[index]), hashes)
			if err != nil || string(got) != outputs[index] {
				t.Fatalf("closed source repair differs: %q %v", got, err)
			}
			for _, data := range [][]byte{nil, []byte(inputs[index] + inputs[index]), []byte(outputs[index])} {
				changed := npmRepairHashes{npmRepairDigest(data), hashes.after}
				if _, err := repairNPMEntry(index, data, changed); !errors.Is(err, ErrInvalid) {
					t.Fatalf("missing, repeated or already derived hunk accepted: %v", err)
				}
			}
			if _, err := repairNPMEntry(index, []byte(inputs[index]+"foreign"), hashes); !errors.Is(err, ErrInvalid) {
				t.Fatalf("changed preimage accepted: %v", err)
			}
			hashes.after = strings.Repeat("0", 64)
			if _, err := repairNPMEntry(index, []byte(inputs[index]), hashes); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unbound derived output accepted: %v", err)
			}
		})
	}
}

func TestGlobRawRoutingPreservesVendorMetadataAndVersion(t *testing.T) {
	t.Parallel()
	output, err := routeGlobRaw([]byte(globBeforeFixture))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if err := errors.Join(json.Unmarshal([]byte(globBeforeFixture), &before), json.Unmarshal(output, &after)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name", "version", "tshy"} {
		if !bytes.Equal(before[key], after[key]) {
			t.Fatalf("glob source identity or build metadata changed: %s", key)
		}
	}
	var exports map[string]json.RawMessage
	if err := json.Unmarshal(after["exports"], &exports); err != nil {
		t.Fatal(err)
	}
	var defaultExport, rawExport any
	if err := errors.Join(json.Unmarshal(exports["."], &defaultExport),
		json.Unmarshal(exports["./raw"], &rawExport)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(defaultExport, rawExport) {
		t.Fatal("glob default differs from its supported modular export")
	}
}

func TestInstallRequiresAllRepairsBeforeFetchingLaterArchives(t *testing.T) {
	t.Parallel()
	for index, name := range npmRepairFileNames() {
		for _, fault := range []string{"missing", "changed", "oversized"} {
			t.Run(name+"/"+fault, func(t *testing.T) {
				t.Parallel()
				contract, bodies := fixture(t)
				contract.Artifacts[0], bodies["npm"] = invalidRepairArchive(t, index, fault)
				root, requests := t.TempDir(), 0
				err := installFixture(t.Context(), root, contract, fixtureClient(t, bodies, &requests))
				if !errors.Is(err, ErrInvalid) || requests != 1 {
					t.Fatalf("incomplete repair reached later archives: requests=%d error=%v", requests, err)
				}
				path := filepath.Join(root, "npm-bootstrap", filepath.FromSlash(name))
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unverified repair output was written: %v", err)
				}
			})
		}
	}
}

func invalidRepairArchive(t *testing.T, selected int, fault string) (Artifact, []byte) {
	t.Helper()
	inputs := fixtureRepairInputs()
	contents := make(map[string]string)
	headers := make([]*tar.Header, 0, len(inputs))
	for index, name := range npmRepairFileNames() {
		data := inputs[index]
		if index == selected {
			switch fault {
			case "missing":
				continue
			case "changed":
				data += "foreign"
			case "oversized":
				data = strings.Repeat("x", npmRepairFileLimit+1)
			default:
				t.Fatalf("unknown repair fault %q", fault)
			}
		}
		path := "package/" + name
		contents[path] = data
		headers = append(headers, &tar.Header{Name: path, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(data))})
	}
	data, expanded := makeArchive(t, headers, contents)
	return artifactFor("npm", "12.2.1", data, expanded, len(headers)), data
}
