package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The unused tag must match the code: a key not marked unused is read
// somewhere, and a key marked unused is read nowhere. "Read" means the
// field's selector, such as ".Downloads.Dir", appears in a non-test Go file
// under internal/ or in main.go. The schema code here works by reflection,
// so it never counts. Code that reads a field another way (s := x.Downloads;
// s.Dir) must be written out or this fails.
func TestUnusedTagMatchesReaders(t *testing.T) {
	var src strings.Builder
	files := []string{"../../main.go"}
	filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	all := src.String()
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
		// \b: ".Downloads.Dir" must not match ".Downloads.DirX".
		if read := regexp.MustCompile(regexp.QuoteMeta(sel) + `\b`).MatchString(all); read == f.unused {
			t.Errorf("%s: read = %v, but unused tag = %v", f.key, read, f.unused)
		}
	}
}
