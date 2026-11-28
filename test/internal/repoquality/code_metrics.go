package repoquality

import (
	"fmt"
	"go/ast"
	"go/scanner"
	"go/token"
	"slices"
	"strings"
)

type codeBodyVisitor struct {
	function *CodeFunction
	loops    int
}

func inspectCodeBody(fn *CodeFunction, body *ast.BlockStmt) {
	fn.Complexity = 1
	ast.Walk(codeBodyVisitor{function: fn}, body)
	slices.Sort(fn.Calls)
}

func (visitor codeBodyVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return nil
	}
	if _, nested := node.(*ast.FuncLit); nested {
		return nil
	}
	if _, statement := node.(ast.Stmt); statement {
		if _, block := node.(*ast.BlockStmt); !block {
			visitor.function.Statements++
		}
	}
	visitor.observeBranch(node)
	if _, loop := node.(*ast.ForStmt); loop {
		visitor.loops++
	}
	if _, loop := node.(*ast.RangeStmt); loop {
		visitor.loops++
	}
	visitor.function.LoopDepth = max(visitor.function.LoopDepth, visitor.loops)
	return visitor
}

func (visitor codeBodyVisitor) observeBranch(node ast.Node) {
	switch item := node.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
		visitor.function.Complexity++
	case *ast.CaseClause:
		if len(item.List) != 0 {
			visitor.function.Complexity++
		}
	case *ast.CommClause:
		if item.Comm != nil {
			visitor.function.Complexity++
		}
	case *ast.BinaryExpr:
		if item.Op == token.LAND || item.Op == token.LOR {
			visitor.function.Complexity++
		}
	case *ast.GoStmt:
		visitor.function.Goroutines++
	case *ast.DeferStmt:
		visitor.function.Defers++
	case *ast.CallExpr:
		visitor.function.Calls = append(visitor.function.Calls, codeExpressionName(item.Fun))
	}
}

func codeExpressionName(expression ast.Expr) string {
	switch item := expression.(type) {
	case *ast.Ident:
		return item.Name
	case *ast.SelectorExpr:
		return codeExpressionName(item.X) + "." + item.Sel.Name
	case *ast.StarExpr:
		return "*" + codeExpressionName(item.X)
	case *ast.IndexExpr:
		return codeExpressionName(item.X)
	case *ast.IndexListExpr:
		return codeExpressionName(item.X)
	case *ast.ParenExpr:
		return codeExpressionName(item.X)
	default:
		return "<dynamic>"
	}
}

func codeFingerprints(
	positions *token.FileSet, function ast.Node, body *ast.BlockStmt, source []byte,
) (string, string, int) {
	start, end := positions.Position(body.Pos()).Offset, positions.Position(body.End()).Offset
	localNames, preserved := codeLocalNames(function)
	var exact, renamed strings.Builder
	var lexer scanner.Scanner
	lexer.Init(token.NewFileSet().AddFile("body", -1, end-start), source[start:end], nil, 0)
	count := 0
	for {
		position, symbol, literal := lexer.Scan()
		if symbol == token.EOF {
			break
		}
		count++
		fmt.Fprintf(&exact, "%s:%s\x00", symbol, literal)
		if symbol == token.IDENT && !preserved[token.Pos(start+int(position))] && localNames[literal] != "" {
			literal = localNames[literal]
		}
		fmt.Fprintf(&renamed, "%s:%s\x00", symbol, literal)
	}
	return codeDigest([]byte(exact.String())), codeDigest([]byte(renamed.String())), count
}

// Candidate normalization is scope-insensitive; typed references must confirm reuse.
func codeLocalNames(function ast.Node) (map[string]string, map[token.Pos]bool) {
	names := make(map[string]string)
	preserved := make(map[token.Pos]bool)
	add := func(identifier *ast.Ident) {
		if identifier.Name != "_" && names[identifier.Name] == "" {
			names[identifier.Name] = fmt.Sprintf("v%d", len(names))
		}
	}
	ast.Inspect(function, func(node ast.Node) bool {
		for _, identifier := range codeBindingNames(node) {
			add(identifier)
		}
		switch item := node.(type) {
		case *ast.SelectorExpr:
			preserved[item.Sel.Pos()] = true
		case *ast.KeyValueExpr:
			if key, ok := item.Key.(*ast.Ident); ok {
				preserved[key.Pos()] = true
			}
		}
		return true
	})
	return names, preserved
}

func codeBindingNames(node ast.Node) []*ast.Ident {
	switch item := node.(type) {
	case *ast.Field:
		return item.Names
	case *ast.ValueSpec:
		return item.Names
	case *ast.AssignStmt:
		if item.Tok == token.DEFINE {
			return codeIdentifiers(item.Lhs)
		}
	case *ast.RangeStmt:
		if item.Tok == token.DEFINE {
			return codeIdentifiers([]ast.Expr{item.Key, item.Value})
		}
	}
	return nil
}

func codeIdentifiers(expressions []ast.Expr) []*ast.Ident {
	var identifiers []*ast.Ident
	for _, expression := range expressions {
		if identifier, ok := expression.(*ast.Ident); ok {
			identifiers = append(identifiers, identifier)
		}
	}
	return identifiers
}

func codeClones(functions []CodeFunction) []CodeClone {
	const minimumCloneTokens = 8
	groups := make(map[string][]CodeFunction)
	for _, fn := range functions {
		if fn.Tokens >= minimumCloneTokens {
			groups["exact:"+fn.ExactBody] = append(groups["exact:"+fn.ExactBody], fn)
			groups["local-renaming:"+fn.RenamedBody] = append(groups["local-renaming:"+fn.RenamedBody], fn)
		}
	}
	var clones []CodeClone
	for key, members := range groups {
		if len(members) < 2 || strings.HasPrefix(key, "local-renaming:") && equalExactBodies(members) {
			continue
		}
		kind, digest, _ := strings.Cut(key, ":")
		clone := CodeClone{Kind: kind, SHA256: digest}
		for _, fn := range members {
			clone.Functions = append(clone.Functions, fn.ID)
		}
		clones = append(clones, clone)
	}
	slices.SortFunc(clones, func(a, b CodeClone) int {
		return strings.Compare(a.Kind+":"+a.SHA256, b.Kind+":"+b.SHA256)
	})
	return clones
}

func equalExactBodies(functions []CodeFunction) bool {
	for _, fn := range functions[1:] {
		if fn.ExactBody != functions[0].ExactBody {
			return false
		}
	}
	return true
}
