package locale

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func TestTextFallback(t *testing.T) {
	for _, lang := range []string{"", "en", "es", "fr", "it", "nl", "pt", "ru", "unknown"} {
		if got := Text(lang, "done"); got != "Done" {
			t.Errorf("%q: %q", lang, got)
		}
	}
	if got := Text("de", "done"); got != "Fertig" {
		t.Fatal(got)
	}
	if got := Text("de", "missing_key"); got != "missing_key" {
		t.Fatal(got)
	}
	if got := Text("ja", "done"); got != "完了" {
		t.Fatal(got)
	}
	if got := Text("ja", "missing_key"); got != "missing_key" {
		t.Fatal(got)
	}
	// Partial catalogs and empty entries follow the same policy as missing languages.
	for _, catalog := range []map[string]map[string]string{{}, {"test": {"done": ""}}} {
		if got := translated("test", "done", catalog, "Done"); got != "Done" {
			t.Fatal(got)
		}
	}
}

func TestAdditionalUILanguageNeedsOnlyACatalog(t *testing.T) {
	uiTranslations["test"] = map[string]string{"done": "Finished"}
	t.Cleanup(func() { delete(uiTranslations, "test") })
	if got := Text("test", "done"); got != "Finished" {
		t.Fatal(got)
	}
	if got := Text("test", "cancel"); got != "Cancel" {
		t.Fatal(got)
	}
}

func TestUICatalogs(t *testing.T) {
	formats := regexp.MustCompile(`%[+# .0-9\[\]-]*[a-zA-Z%]`)
	for lang, catalog := range uiTranslations {
		for key, value := range catalog {
			english, ok := uiEnglish[key]
			if !ok || english == "" {
				t.Errorf("%s: unknown key %s", lang, key)
			}
			if value == "" {
				t.Errorf("%s: empty translation %s", lang, key)
			}
			if !reflect.DeepEqual(formats.FindAllString(english, -1), formats.FindAllString(value, -1)) {
				t.Errorf("%s/%s: format arguments differ", lang, key)
			}
		}
	}
	if got := fmt.Sprintf(Text("ja", "snooze_d_min"), 9); got != "9分スヌーズ" {
		t.Fatal(got)
	}
}

// Catch misspelled or removed keys at every drawing call, including build-tagged files.
func TestDrawingTranslationKeysExist(t *testing.T) {
	paths, err := filepath.Glob("../../feature/display/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "tr" {
				return true
			}
			if len(call.Args) != 1 {
				t.Errorf("%s: tr needs one key", path)
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				if index, indexed := call.Args[0].(*ast.IndexExpr); indexed {
					if table, named := index.X.(*ast.Ident); named && (table.Name == "categoryNameKeys" || table.Name == "categoryTitleKeys" || table.Name == "categoryBlurbKeys") {
						return true // These presentation tables are checked below.
					}
				}
				t.Errorf("%s: use a stable literal key", path)
				return true
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := uiEnglish[key]; !ok {
				t.Errorf("%s: unknown UI key %q", path, key)
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			if declaration, ok := n.(*ast.GenDecl); ok {
				ast.Inspect(declaration, func(child ast.Node) bool {
					if call, ok := child.(*ast.CallExpr); ok {
						if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "tr" {
							t.Errorf("%s: translation must be evaluated when drawn, not during package initialization", path)
						}
					}
					return true
				})
			}
			check := func(expr ast.Expr) {
				lit, ok := expr.(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: category keys must be literals", path)
					return
				}
				key, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := uiEnglish[key]; !ok {
					t.Errorf("%s: unknown category key %q", path, key)
				}
			}
			if spec, ok := n.(*ast.ValueSpec); ok && len(spec.Names) == 1 && (spec.Names[0].Name == "categoryNameKeys" || spec.Names[0].Name == "categoryTitleKeys" || spec.Names[0].Name == "categoryBlurbKeys") {
				if values, ok := spec.Values[0].(*ast.CompositeLit); ok {
					for _, value := range values.Elts {
						check(value)
					}
				}
			}
			if assign, ok := n.(*ast.AssignStmt); ok && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 {
				if index, ok := assign.Lhs[0].(*ast.IndexExpr); ok {
					if table, ok := index.X.(*ast.Ident); ok && table.Name == "categoryBlurbKeys" {
						check(assign.Rhs[0])
					}
				}
			}
			return true
		})
	}
}

func TestJapaneseDatesAndWeather(t *testing.T) {
	at := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	for got, want := range map[string]string{
		LongDate(at, "ja"): "1月5日（月曜日）", ShortDate(at, "ja"): "1/5（月）",
		MonthYear(at, "ja"): "2026年1月",
		MonthDay(at, "ja"):  "1月5日", PhotoDate(at, "ja"): "2026年1月5日",
		DayAndNumber(at, "ja"): "5日（月）", Sky("sunny", "ja"): "晴れ",
	} {
		if got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
}
