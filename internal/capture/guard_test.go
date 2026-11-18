package capture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// kit's own binary never takes in what draws the art: the capture's runs and
// fixed clocks are for the capture tool alone. Read from the module's
// source, every file of every package kit imports, whatever its build tags.
func TestKitNeverImportsTheCapture(t *testing.T) {
	const module = "github.com/leeovery/kit"
	never := []string{module + "/internal/capture", module + "/internal/render/rendertest"}
	root := filepath.Join("..", "..")
	seen := map[string]bool{}
	var visit func(pkg string, via []string)
	visit = func(pkg string, via []string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		dir := filepath.Join(root, strings.TrimPrefix(pkg, module))
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if !strings.HasPrefix(path, module+"/") {
					continue
				}
				chain := append(append([]string{}, via...), path)
				for _, n := range never {
					if path == n {
						t.Errorf("kit imports %s: %s", n, strings.Join(chain, " → "))
					}
				}
				visit(path, chain)
			}
		}
	}
	visit(module+"/cmd/kit", []string{module + "/cmd/kit"})
	if len(seen) < 10 {
		t.Fatalf("read only %d packages from cmd/kit: the walk isn't reading kit's imports", len(seen))
	}
}
