package quality

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryNamedGoDeclarationHasAContractComment enforces the design's hand-written declaration rule.
func TestEveryNamedGoDeclarationHasAContractComment(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() &&
			(entry.Name() == ".git" || entry.Name() == ".wt" || entry.Name() == "node_modules" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		checkFile(t, root, path)
		return nil
	})
	require.NoError(t, err)
}

// repositoryRoot resolves the module root relative to this test source.
func repositoryRoot(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

// checkFile parses one hand-written Go file and reports missing declaration comments.
func checkFile(t testing.TB, root string, filename string) {
	t.Helper()
	file, err := os.Open(filename)
	require.NoError(t, err)
	scanner := bufio.NewScanner(file)
	generated := scanner.Scan() && strings.Contains(scanner.Text(), "Code generated")
	require.NoError(t, scanner.Err())
	require.NoError(t, file.Close())
	if generated {
		return
	}

	set := token.NewFileSet()
	syntax, err := parser.ParseFile(set, filename, nil, parser.ParseComments)
	require.NoError(t, err)
	for _, declaration := range syntax.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			assertDeclarationComment(t, root, filename, set, value.Name.Name, value.Doc)
		case *ast.GenDecl:
			for _, spec := range value.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				doc := typeSpec.Doc
				if doc == nil {
					doc = value.Doc
				}
				assertDeclarationComment(t, root, filename, set, typeSpec.Name.Name, doc)
			}
		}
	}
}

// assertDeclarationComment checks declaration adjacency and identifier-prefixed contract text.
func assertDeclarationComment(
	t testing.TB,
	root string,
	filename string,
	set *token.FileSet,
	name string,
	doc *ast.CommentGroup,
) {
	t.Helper()
	position := set.PositionFor(docPosition(doc), true)
	location := filepath.ToSlash(filename)
	if relative, err := filepath.Rel(root, filename); err == nil {
		location = filepath.ToSlash(relative)
	}
	assert.NotNil(t, doc, "%s: declaration %s lacks a comment", location, name)
	if doc == nil {
		return
	}
	text := strings.TrimSpace(doc.Text())
	assert.True(
		t,
		strings.HasPrefix(text, name+" ") || strings.HasPrefix(text, name+"\n"),
		"%s:%d: declaration comment for %s must begin with its name",
		location,
		position.Line,
		name,
	)
}

// docPosition returns a safe token position for optional comments.
func docPosition(doc *ast.CommentGroup) token.Pos {
	if doc == nil {
		return token.NoPos
	}
	return doc.Pos()
}
