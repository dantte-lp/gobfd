package repoquality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

// CodeFile identifies one complete lexical input, regardless of build selection.
type CodeFile struct {
	Path      string `json:"path"`
	Package   string `json:"package"`
	SHA256    string `json:"sha256"`
	Build     string `json:"build"`
	Generated bool   `json:"generated"`
	Test      bool   `json:"test"`
	Category  string `json:"category"`
}

// CodeFunction records observations; neither purity nor runtime cost is inferred.
type CodeFunction struct {
	ID          string       `json:"id"`
	Path        string       `json:"path"`
	Name        string       `json:"name"`
	Receiver    string       `json:"receiver"`
	Line        int          `json:"line"`
	EndLine     int          `json:"end_line"`
	Column      int          `json:"column"`
	Kind        string       `json:"kind"`
	Generated   bool         `json:"generated"`
	Test        bool         `json:"test"`
	Category    string       `json:"category"`
	Exported    bool         `json:"exported"`
	SHA256      string       `json:"sha256"`
	ExactBody   string       `json:"exact_body_sha256"`
	RenamedBody string       `json:"renamed_body_sha256"`
	Tokens      int          `json:"tokens"`
	Complexity  int          `json:"complexity"`
	Statements  int          `json:"statements"`
	LoopDepth   int          `json:"explicit_loop_depth"`
	Goroutines  int          `json:"goroutines"`
	Defers      int          `json:"defers"`
	Calls       []string     `json:"syntactic_calls"`
	Idempotency string       `json:"idempotency"`
	Runtime     string       `json:"runtime_measurement"`
	CPU         *CodeCPUCost `json:"cpu"`
}

// CodeImport is a file-local package dependency, including test/build variants.
type CodeImport struct {
	From string `json:"from"`
	To   string `json:"to"`
	File string `json:"file"`
}

// CodeClone is a structural candidate, never a proof of semantic equivalence.
type CodeClone struct {
	Kind      string   `json:"kind"`
	SHA256    string   `json:"sha256"`
	Functions []string `json:"functions"`
}

// CodeReport is a deterministic quality vector over an explicit input set.
type CodeReport struct {
	Schema                   string          `json:"schema"`
	SourceSHA256             string          `json:"source_sha256"`
	Files                    []CodeFile      `json:"files"`
	Functions                []CodeFunction  `json:"functions"`
	Imports                  []CodeImport    `json:"imports"`
	Clones                   []CodeClone     `json:"clones"`
	HighComplexity           int             `json:"high_complexity_non_test_functions"`
	ProductionHighComplexity int             `json:"high_complexity_production_functions"`
	CPUProfile               *CodeCPUProfile `json:"cpu_profile,omitempty"`
}

// DiscoverGoSources includes nested modules and build-excluded Go files.
func DiscoverGoSources(ctx context.Context, directory string) (_ []string, result error) {
	var files []string
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open Go source root: %w", err)
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk Go input %s: %w", name, walkErr)
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("discover Go inputs: %w", contextErr)
		}
		if entry.IsDir() && name != "." && excludedCodeDirectory(name) {
			return fs.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".go") {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk Go source tree: %w", err)
	}
	slices.Sort(files)
	return files, nil
}

func excludedCodeDirectory(name string) bool {
	return strings.HasPrefix(path.Base(name), ".") || slices.Contains([]string{
		"vendor", "node_modules", "reports", "bin", "build", "dist",
	}, name)
}

// InspectCode parses each supplied source exactly once and rejects partial input.
func InspectCode(ctx context.Context, directory string, inputs []string) (_ CodeReport, result error) {
	var report CodeReport
	if len(inputs) == 0 {
		return report, fmt.Errorf("empty Go source inventory: %w", fs.ErrInvalid)
	}
	const maximumCodeFiles = 10000
	if len(inputs) > maximumCodeFiles {
		return report, fmt.Errorf("go input count exceeds %d: %w", maximumCodeFiles, fs.ErrInvalid)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return report, fmt.Errorf("open code inventory root: %w", err)
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	report.Schema = "gobfd-code-quality/v1"
	names := slices.Clone(inputs)
	slices.Sort(names)
	combined := sha256.New()
	const maximumSourceBytes = 256 << 20
	var sourceBytes int
	for i, name := range names {
		if !fs.ValidPath(name) || path.Ext(name) != ".go" || (i > 0 && names[i-1] == name) {
			return CodeReport{}, fmt.Errorf("invalid or duplicate Go source %q: %w", name, fs.ErrInvalid)
		}
		if err := ctx.Err(); err != nil {
			return CodeReport{}, fmt.Errorf("inspect Go inputs: %w", err)
		}
		data, err := readRepositoryFile(root, name)
		if err != nil {
			return CodeReport{}, err
		}
		sourceBytes += len(data)
		if sourceBytes > maximumSourceBytes {
			return CodeReport{}, fmt.Errorf("go source budget exceeds %d: %w", maximumSourceBytes, fs.ErrInvalid)
		}
		if err := report.addCodeFile(name, data); err != nil {
			return CodeReport{}, err
		}
		// hash.Hash.Write cannot fail; include a separator and a fixed-width content digest.
		combined.Write([]byte(name + "\x00"))    // fail-silent-ok: hash.Hash.Write always returns nil error.
		combined.Write([]byte(codeDigest(data))) // fail-silent-ok: hash.Hash.Write always returns nil error.
	}
	report.SourceSHA256 = hex.EncodeToString(combined.Sum(nil))
	report.Clones = codeClones(report.Functions)
	return report, nil
}

func (report *CodeReport) addCodeFile(name string, data []byte) error {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, name, data, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse Go input %s: %w", name, err)
	}
	input := CodeFile{
		Path: name, Package: path.Dir(name) + ":" + file.Name.Name,
		SHA256: codeDigest(data), Generated: ast.IsGenerated(file), Test: strings.HasSuffix(name, "_test.go"),
	}
	input.Category = codeCategory(input)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if expression, ok := strings.CutPrefix(comment.Text, "//go:build "); ok {
				input.Build = expression
			}
		}
	}
	report.Files = append(report.Files, input)
	for _, dependency := range file.Imports {
		importPath, err := strconv.Unquote(dependency.Path.Value)
		if err != nil {
			return fmt.Errorf("decode import in %s: %w", name, err)
		}
		report.Imports = append(report.Imports, CodeImport{From: input.Package, To: importPath, File: name})
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch fn := node.(type) {
		case *ast.FuncDecl:
			receiver := ""
			if fn.Recv != nil {
				receiver = codeExpressionName(fn.Recv.List[0].Type)
			}
			report.addCodeFunction(input, positions, data, fn, fn.Body, fn.Name.Name, receiver, "declaration")
		case *ast.FuncLit:
			report.addCodeFunction(input, positions, data, fn, fn.Body, "literal", "", "literal")
		}
		return true
	})
	return nil
}

func (report *CodeReport) addCodeFunction(
	file CodeFile, positions *token.FileSet, data []byte, node ast.Node, body *ast.BlockStmt,
	name, receiver, kind string,
) {
	start, end := positions.PositionFor(node.Pos(), false), positions.PositionFor(node.End(), false)
	fn := CodeFunction{
		ID:   fmt.Sprintf("%s:%d:%d:%s:%s", file.Path, start.Line, start.Column, receiver, name),
		Path: file.Path, Name: name, Receiver: receiver, Line: start.Line, EndLine: end.Line,
		Column: start.Column, Kind: kind, Generated: file.Generated, Test: file.Test,
		Category: file.Category,
		Exported: kind == "declaration" && ast.IsExported(name),
		SHA256:   codeDigest(data[start.Offset:end.Offset]), Idempotency: "review_required", Runtime: "not_observed",
	}
	if body != nil {
		inspectCodeBody(&fn, body)
		fn.ExactBody, fn.RenamedBody, fn.Tokens = codeFingerprints(positions, node, body, data)
	}
	if !file.Generated && !file.Test && fn.Complexity > 15 {
		report.HighComplexity++
		if file.Category == "production" {
			report.ProductionHighComplexity++
		}
	}
	report.Functions = append(report.Functions, fn)
}

func codeCategory(file CodeFile) string {
	if file.Generated {
		return "generated"
	}
	if file.Test {
		return "test"
	}
	if strings.HasPrefix(file.Path, "test/") || strings.HasPrefix(file.Path, "tools/") ||
		strings.HasPrefix(file.Path, "scripts/") || strings.HasPrefix(file.Path, "internal/cmd/") ||
		strings.HasPrefix(file.Path, "internal/testutil/") {
		return "tooling"
	}
	return "production"
}

func codeDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
