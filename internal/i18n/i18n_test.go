package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// usedKeys returns the keys the module's code gives T and Errorf as
// literals.
func usedKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "build" || name == "dist" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "T" && sel.Sel.Name != "Errorf") {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "i18n" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: i18n.%s without a literal key", path, sel.Sel.Name)
				return true
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: %v", path, err)
				return true
			}
			keys = append(keys, key)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

var verb = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*\d*(?:\.\d+)?[a-zA-Z%]`)

// verbs returns the verbs of a format, without their argument indexes, in
// argument order.
func verbs(format string) []string {
	var out []string
	next := 1
	type arg struct {
		n int
		v string
	}
	var args []arg
	for _, v := range verb.FindAllString(format, -1) {
		if v == "%%" {
			continue
		}
		n := next
		if i := strings.Index(v, "["); i >= 0 {
			n, _ = strconv.Atoi(v[i+1 : strings.Index(v, "]")])
			v = v[:i] + v[strings.Index(v, "]")+1:]
		}
		args = append(args, arg{n, v})
		next = n + 1
	}
	slices.SortStableFunc(args, func(a, b arg) int { return a.n - b.n })
	for _, a := range args {
		out = append(out, a.v)
	}
	return out
}

func TestCatalogs(t *testing.T) {
	used := usedKeys(t)
	if len(used) < 50 {
		t.Fatalf("found only %d keys: is the walk right?", len(used))
	}
	for lang, cat := range catalogs {
		for _, k := range used {
			s, ok := cat[k]
			if !ok {
				t.Errorf("%s: missing %q", lang, k)
				continue
			}
			if got, want := verbs(s), verbs(k); !slices.Equal(got, want) {
				t.Errorf("%s: %q has verbs %v, want %v", lang, s, got, want)
			}
		}
		for k := range cat {
			if !slices.Contains(used, k) {
				t.Errorf("%s: %q is not used", lang, k)
			}
		}
	}
}

func TestVerbs(t *testing.T) {
	if got := verbs("%[2]s：%[1]d 个"); !slices.Equal(got, []string{"%d", "%s"}) {
		t.Errorf("verbs = %v", got)
	}
}

func TestMatch(t *testing.T) {
	for locale, want := range map[string]string{
		"zh-Hans-CN": "zh-Hans", "zh-CN": "zh-Hans", "zh": "zh-Hans",
		"zh-Hant-TW": "zh-Hant", "zh-TW": "zh-Hant", "zh_HK": "zh-Hant",
		"ja-JP": "ja", "ko-KR": "ko", "ru-RU": "ru", "en-US": "en", "fr": "en", "": "en",
	} {
		if got := Match(locale); got != want {
			t.Errorf("Match(%q) = %q, want %q", locale, got, want)
		}
	}
}

func TestT(t *testing.T) {
	defer Set("en")
	Set("zh-Hans")
	if got := T("Sync now"); got != "立即同步" {
		t.Errorf("T = %q", got)
	}
	if got := T("No such text %d", 3); got != "No such text 3" {
		t.Errorf("a missing text: %q", got)
	}
	Set("xx")
	if Current() != "en" {
		t.Errorf("Set(xx): %q", Current())
	}
}
