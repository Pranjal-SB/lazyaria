package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The unused tag must match the code: a key not marked unused is read
// somewhere, and a key marked unused is read nowhere. "Read" means a
// selector chain such as s.Downloads.Dir in a non-test Go file under
// internal/ or in main.go, not on the left of an assignment. Comments and
// strings do not count. The schema code here works by reflection, so it
// never counts for a struct field. Code that reads a field another way
// (d := s.Downloads; d.Dir) must be written out or this fails.
//
// keys.* and theme.* are not checked: they are map entries, and the daemon
// copies the whole maps (daemon_config.go), which a selector cannot tell
// from a reader of a binding.
func TestUnusedTagMatchesReaders(t *testing.T) {
	files := []string{"../../main.go"}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]bool{} // ".Downloads.Dir", ".Dir", ...
	fset := token.NewFileSet()
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		pkgs := map[string]bool{}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			name := filepath.Base(p)
			if im.Name != nil {
				name = im.Name.Name
			}
			pkgs[name] = true
		}
		written := map[ast.Expr]bool{}
		ast.Inspect(af, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok && as.Tok == token.ASSIGN {
				for _, l := range as.Lhs {
					written[l] = true
				}
			}
			se, ok := n.(*ast.SelectorExpr)
			if !ok || written[se] {
				return true
			}
			chain := ""
			var e ast.Expr = se
			for x, ok := e.(*ast.SelectorExpr); ok; x, ok = e.(*ast.SelectorExpr) {
				chain = "." + x.Sel.Name + chain
				e = x.X
			}
			if id, ok := e.(*ast.Ident); ok && pkgs[id.Name] {
				return true // pkg.Name, such as maps.Keys
			}
			for s := chain; s != ""; {
				read[s] = true
				i := strings.Index(s[1:], ".")
				if i < 0 {
					break
				}
				s = s[i+1:]
			}
			return true
		})
	}
	root := reflect.TypeOf(Settings{})
	for _, f := range fields() {
		// version marks the file format; nothing reads it until the format changes.
		if f.idx == nil || f.key == "version" {
			continue
		}
		sel := ""
		for i := range f.idx {
			sel += "." + root.FieldByIndex(f.idx[:i+1]).Name
		}
		if read[sel] == f.unused {
			t.Errorf("%s: read = %v, but unused tag = %v", f.key, read[sel], f.unused)
		}
	}
}
