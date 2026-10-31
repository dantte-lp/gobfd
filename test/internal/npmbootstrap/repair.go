package npmbootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The original npm archive keeps its registry SRI. These separate bindings
// identify the three derived files, without changing npm/minimatch/glob versions.
// The preprocessing backport is upstream commit:
// https://github.com/isaacs/minimatch/commit/77a0f5c4b80bdc641c3c930a1d9edf5ad2db57f8
// glob 13.0.6 already publishes the modular entrypoints as its ./raw export.
const npmRepairFileLimit = 64 << 10

type npmRepairHashes struct {
	before, after string
}

func npmRepairArtifact() Artifact {
	return Artifact{
		Name: "npm", Version: "12.2.1",
		Integrity:    "sha512-bKCmFRVT/BB+YPEYbZwQ1XUI3N5t4xKSpU1MxOjo7z3iWzKBJ0ZXRC1xFdEN+TC7N5TdywaBzf2dMXGJq/e2PQ==",
		ArchiveBytes: 3062824, ExpandedBytes: 12430582, Entries: 1942,
	}
}

func npmRepairFileNames() [3]string {
	return [3]string{
		"node_modules/minimatch/dist/commonjs/index.js",
		"node_modules/minimatch/dist/esm/index.js",
		"node_modules/glob/package.json",
	}
}

func npmRepairSourceHashes() [3]npmRepairHashes {
	return [3]npmRepairHashes{
		{
			"d224f71f20d7c206fcc5b7e9f747324c7698345a0b2c28d717dd9cf698306783",
			"fd122557d6d3ee306dbddd6b7cf7ac313dd596eaa9eb69eb9c64f0b60f7d5bcc",
		},
		{
			"59821ec2ce0d3df968292036e5c831ad322b33e9c33a949ecbc9dfe125d165be",
			"7980a130fa20a7df65abf2e7d37a2316213a0f6c1274d6a88f2701d061f87c74",
		},
		{
			"5a28ef9a1f5252feb47ffe288a6350fe99b18311462e6a9cdacbdd0bb4be6be9",
			"41d6e0be2b4dd2dd89d10201030039d76597c5f98139f9515e6f7430f1a48414",
		},
	}
}

func writeNPMEntry(root *os.Root, target, name string, reader io.Reader, size int64, hashes [3]npmRepairHashes) error {
	for index, selected := range npmRepairFileNames() {
		if name != selected {
			continue
		}
		if size > npmRepairFileLimit {
			return fmt.Errorf("npm repair input exceeds its file bound: %w", ErrInvalid)
		}
		data, err := io.ReadAll(io.LimitReader(reader, size+1))
		if err != nil || int64(len(data)) != size {
			return fmt.Errorf("read npm repair input: %w", errors.Join(err, ErrInvalid))
		}
		repaired, err := repairNPMEntry(index, data, hashes[index])
		if err != nil {
			return fmt.Errorf("repair npm file %s: %w", name, err)
		}
		path := filepath.Join(target, filepath.FromSlash(name))
		return writeEntry(root, path, bytes.NewReader(repaired), int64(len(repaired)))
	}
	return writeEntry(root, filepath.Join(target, filepath.FromSlash(name)), reader, size)
}

func verifyNPMRepairPresence(seen map[string]bool) error {
	for _, name := range npmRepairFileNames() {
		if !seen[name] {
			return fmt.Errorf("npm archive omits required repair file %s: %w", name, ErrInvalid)
		}
	}
	return nil
}

func repairNPMEntry(index int, data []byte, hashes npmRepairHashes) ([]byte, error) {
	if npmRepairDigest(data) != hashes.before {
		return nil, fmt.Errorf("npm repair preimage differs from its source binding: %w", ErrInvalid)
	}
	var repaired []byte
	var err error
	switch index {
	case 0, 1:
		repaired, err = backportMinimatch(data)
	case 2:
		repaired, err = routeGlobRaw(data)
	default:
		return nil, fmt.Errorf("unknown npm repair file: %w", ErrInvalid)
	}
	if err != nil {
		return nil, err
	}
	if npmRepairDigest(repaired) != hashes.after {
		return nil, fmt.Errorf("npm repair output differs from its derived binding: %w", ErrInvalid)
	}
	return repaired, nil
}

func npmRepairDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func backportMinimatch(data []byte) ([]byte, error) {
	for _, hunk := range [][2]string{
		{
			"    firstPhasePreProcess(globParts) {\n",
			"    firstPhasePreProcess(globParts) {\n        const { braceExpandMax = 100_000 } = this.options;\n",
		},
		{
			"                        p2 === '..') {",
			"                        p2 === '..' ||\n                        globParts.length >= braceExpandMax) {",
		},
		{
			"    secondPhasePreProcess(globParts) {\n",
			"    secondPhasePreProcess(globParts) {\n        if (globParts.length > 1000) return globParts;\n",
		},
		// The original map describes the unmodified source, so detach it.
		{"//# sourceMappingURL=index.js.map", ""},
	} {
		var err error
		data, err = replaceNPMRepairHunk(data, hunk[0], hunk[1])
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func routeGlobRaw(data []byte) ([]byte, error) {
	marker := []byte("\n  \"exports\": {")
	if bytes.Count(data, marker) != 1 {
		return nil, fmt.Errorf("npm glob runtime exports are ambiguous: %w", ErrInvalid)
	}
	before, after, _ := bytes.Cut(data, marker)
	for _, entry := range [][2]string{{"main", "commonjs"}, {"module", "esm"}} {
		path := "./dist/" + entry[1] + "/index"
		var err error
		before, err = replaceNPMRepairHunk(before,
			"  \""+entry[0]+"\": \""+path+".min.js\"", "  \""+entry[0]+"\": \""+path+".js\"")
		if err != nil {
			return nil, err
		}
		after, err = replaceNPMRepairHunk(after, "\"default\": \""+path+".min.js\"", "\"default\": \""+path+".js\"")
		if err != nil {
			return nil, err
		}
	}
	return bytes.Join([][]byte{before, after}, marker), nil
}

func replaceNPMRepairHunk(data []byte, before, after string) ([]byte, error) {
	if bytes.Count(data, []byte(before)) != 1 {
		return nil, fmt.Errorf("npm repair requires one exact upstream hunk: %w", ErrInvalid)
	}
	return bytes.Replace(data, []byte(before), []byte(after), 1), nil
}
