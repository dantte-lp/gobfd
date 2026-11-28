package npmbootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestParseRejectsAmbiguousIncompleteAndUnboundedContracts(t *testing.T) {
	t.Parallel()
	contract, _ := fixture(t)
	valid, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(valid); err != nil {
		t.Fatalf("valid contract: %v", err)
	}
	for name, data := range map[string][]byte{
		"duplicate top": bytes.Replace(valid,
			[]byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1),
		"field alias":        bytes.Replace(valid, []byte(`"schema_version"`), []byte(`"Schema_Version"`), 1),
		"artifact alias":     bytes.Replace(valid, []byte(`"integrity"`), []byte(`"Integrity"`), 1),
		"duplicate artifact": bytes.Replace(valid, []byte(`"name":"npm"`), []byte(`"name":"npm","name":"npm"`), 1),
		"missing schema":     bytes.Replace(valid, []byte(`"schema_version":1,`), nil, 1),
		"missing identity":   bytes.Replace(valid, []byte(`"name":"npm",`), nil, 1),
		"missing bound":      bytes.Replace(valid, []byte(`,"entries":6`), nil, 1),
		"null artifact":      []byte(`{"schema_version":1,"artifacts":[null,null,null]}`),
		"trailing":           append(bytes.Clone(valid), []byte(`{}`)...),
		"bad version":        bytes.Replace(valid, []byte(`"version":"12.2.1"`), []byte(`"version":"12.02.1"`), 1),
		"registry escape":    bytes.Replace(valid, []byte(`"name":"undici"`), []byte(`"name":"../undici"`), 1),
		"bad integrity":      bytes.Replace(valid, []byte(`sha512-`), []byte(`sha256-`), 1),
		"entry bound":        bytes.Replace(valid, []byte(`"entries":6`), []byte(`"entries":4097`), 1),
		"huge":               bytes.Repeat([]byte(" "), contractLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(data); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want invalid contract", err)
			}
		})
	}
}

func TestParseRejectsInvalidResourceContracts(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*Artifact){
		"compressed": func(a *Artifact) { a.ArchiveBytes = archiveLimit + 1 },
		"expanded":   func(a *Artifact) { a.ExpandedBytes = expandedLimit + 1 },
		"entries":    func(a *Artifact) { a.Entries = entryLimit + 1 },
		"negative":   func(a *Artifact) { a.ArchiveBytes = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			contract, _ := fixture(t)
			change(&contract.Artifacts[0])
			data, err := json.Marshal(contract)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(data); !errors.Is(err, ErrInvalid) {
				t.Fatalf("resource contract error=%v", err)
			}
		})
	}
}

func TestParseRejectsEscapedIntegrityNewlines(t *testing.T) {
	t.Parallel()
	for _, newline := range []string{"\r", "\n"} {
		t.Run(newline, func(t *testing.T) {
			t.Parallel()
			contract, _ := fixture(t)
			contract.Artifacts[0].Integrity = strings.Replace(contract.Artifacts[0].Integrity, "sha512-", "sha512-"+newline, 1)
			data, err := json.Marshal(contract)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(data); !errors.Is(err, ErrInvalid) {
				t.Fatalf("noncanonical integrity error = %v, want invalid contract", err)
			}
		})
	}
}

func TestInstallVerifiesAndReplacesOnlyPinnedBundledPackages(t *testing.T) {
	t.Parallel()
	contract, bodies := fixture(t)
	root := t.TempDir()
	requests := 0
	client := fixtureClient(t, bodies, &requests)
	if err := installFixture(t.Context(), root, contract, client); err != nil {
		t.Fatal(err)
	}
	if requests != artifactCount {
		t.Fatalf("requests = %d", requests)
	}
	for name, want := range map[string]string{
		CLIPath: "unchanged upstream npm",
		"npm-bootstrap/node_modules/undici/package.json":     "patched undici",
		"npm-bootstrap/node_modules/ip-address/package.json": "patched ip-address",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(data) != want {
			t.Fatalf("installed %s mismatch: %v", name, err)
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("private regular file %s: %v", name, err)
		}
	}
	outputs := fixtureRepairOutputs()
	for index, name := range npmRepairFileNames() {
		data, err := os.ReadFile(filepath.Join(root, "npm-bootstrap", filepath.FromSlash(name)))
		if err != nil || string(data) != outputs[index] {
			t.Fatalf("mandatory repair %s mismatch: %v", name, err)
		}
	}
	oldUndici := filepath.Join(root, "npm-bootstrap", "node_modules", "undici", "old.js")
	if _, err := os.Lstat(oldUndici); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old bundled dependency retained: %v", err)
	}
}

func TestInstallRejectsHashLinksTraversalDuplicatesAndBounds(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"hash", "traversal", "absolute", "backslash", "symlink", "hardlink",
		"duplicate", "expanded", "entries", "compressed", "gzip",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			headers := []*tar.Header{{Name: "package/bin/npm-cli.js", Mode: 0o644, Typeflag: tar.TypeReg, Size: 1}}
			switch name {
			case "traversal":
				headers[0].Name = "package/../escape"
			case "absolute":
				headers[0].Name = "/package/escape"
			case "backslash":
				headers[0].Name = "package/bin\\escape"
			case "symlink":
				headers[0].Typeflag, headers[0].Linkname, headers[0].Size = tar.TypeSymlink, "../../escape", 0
			case "hardlink":
				headers[0].Typeflag, headers[0].Linkname, headers[0].Size = tar.TypeLink, "../../escape", 0
			case "duplicate":
				copyHeader := *headers[0]
				headers = append(headers, &copyHeader)
			default:
			}
			if name == "symlink" || name == "hardlink" {
				headers = append(headers, &tar.Header{Name: "package/safe.txt", Mode: 0o644, Typeflag: tar.TypeReg, Size: 1})
			}
			data, expanded, count := makeRepairedFixtureArchive(t, headers)
			contract, bodies := fixture(t)
			contract.Artifacts[0] = artifactFor("npm", "12.2.1", data, expanded, count)
			bodies["npm"] = data
			switch name {
			case "hash":
				contract.Artifacts[0].Integrity = "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, sha512.Size))
			case "expanded":
				contract.Artifacts[0].ExpandedBytes++
			case "entries":
				contract.Artifacts[0].Entries++
			case "compressed":
				bodies["npm"] = append(bytes.Clone(data), 'x')
			case "gzip":
				data[len(data)-1] ^= 1
				contract.Artifacts[0] = artifactFor("npm", "12.2.1", data, expanded, count)
			default:
			}
			requests := 0
			if err := installFixture(t.Context(), t.TempDir(), contract, fixtureClient(t, bodies, &requests)); err == nil {
				t.Fatal("unsafe bootstrap archive accepted")
			}
			if requests != 1 {
				t.Fatalf("invalid first archive fetched later packages: %d requests", requests)
			}
		})
	}
}

func TestInstallRejectsRedirectAndCancellation(t *testing.T) {
	t.Parallel()
	contract, _ := fixture(t)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://foreign.invalid/package.tgz"}},
			Body: io.NopCloser(strings.NewReader("")), Request: request,
		}, nil
	})}
	if err := installFixture(t.Context(), t.TempDir(), contract, client); !errors.Is(err, ErrInvalid) || calls != 1 {
		t.Fatalf("redirect error = %v, requests=%d", err, calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})
	if err := installFixture(ctx, t.TempDir(), contract, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

func fixture(t *testing.T) (Contract, map[string][]byte) {
	t.Helper()
	contents := map[string]string{
		"package/bin/npm-cli.js":                 "unchanged upstream npm",
		"package/node_modules/undici/old.js":     "old undici",
		"package/node_modules/ip-address/old.js": "old ip-address",
	}
	repairFiles := npmRepairFileNames()
	names := make([]string, 0, len(contents)+len(repairFiles))
	names = append(names,
		"package/bin/npm-cli.js", "package/node_modules/undici/old.js", "package/node_modules/ip-address/old.js",
	)
	inputs := fixtureRepairInputs()
	for index, name := range repairFiles {
		names = append(names, "package/"+name)
		contents["package/"+name] = inputs[index]
	}
	headers := make([]*tar.Header, 0, len(contents))
	for _, name := range names {
		headers = append(headers, &tar.Header{
			Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(contents[name])),
		})
	}
	data, expanded := makeArchive(t, headers, contents)
	contract := Contract{
		SchemaVersion: 1, Artifacts: []Artifact{artifactFor("npm", "12.2.1", data, expanded, len(headers))},
	}
	bodies := map[string][]byte{"npm": data}
	for _, name := range []string{"undici", "ip-address"} {
		content := "patched " + name
		artifactHeaders := []*tar.Header{{
			Name: "package/package.json", Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(content)),
		}}
		data, expanded := makeArchive(t, artifactHeaders, map[string]string{"package/package.json": content})
		version := "6.29.1"
		if name == "ip-address" {
			version = "10.7.4"
		}
		contract.Artifacts = append(contract.Artifacts, artifactFor(name, version, data, expanded, 1))
		bodies[name] = data
	}
	return contract, bodies
}

func artifactFor(name, version string, data []byte, expanded int64, entries int) Artifact {
	digest := sha512.Sum512(data)
	return Artifact{
		Name: name, Version: version, Integrity: "sha512-" + base64.StdEncoding.EncodeToString(digest[:]),
		ArchiveBytes: int64(len(data)), ExpandedBytes: expanded, Entries: entries,
	}
}

func makeArchive(t *testing.T, headers []*tar.Header, contents map[string]string) ([]byte, int64) {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	writer := tar.NewWriter(compressed)
	var expanded int64
	for _, header := range headers {
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			data := contents[header.Name]
			if data == "" {
				data = strings.Repeat("x", int(header.Size))
			}
			if _, err := io.WriteString(writer, data); err != nil {
				t.Fatal(err)
			}
		}
		expanded += header.Size
	}
	if err := errors.Join(writer.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	return output.Bytes(), expanded
}

func fixtureClient(t *testing.T, bodies map[string][]byte, calls *int) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		(*calls)++
		if request.URL.Scheme != "https" || request.URL.Host != "registry.npmjs.org" ||
			request.Header.Get("Authorization") != "" {
			t.Fatal("bootstrap request escaped unauthenticated official registry")
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(request.URL.Path, "/"), "/")
		data, ok := bodies[name]
		if !ok {
			t.Fatal("unexpected package request")
		}
		return &http.Response{
			StatusCode: http.StatusOK, ContentLength: -1,
			Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header), Request: request,
		}, nil
	})}
}
