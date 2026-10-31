package locale

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTextFallback(t *testing.T) {
	for _, lang := range []string{"", "en", "nl", "pt", "ru", "unknown"} {
		if got := Text(lang, "done"); got != "Done" {
			t.Errorf("%q: %q", lang, got)
		}
	}
	if got := Text("es", "done"); got != "Listo" {
		t.Fatal(got)
	}
	if got := Text("de", "done"); got != "Fertig" {
		t.Fatal(got)
	}
	if got := Text("fr", "done"); got != "Terminé" {
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

// The sunrise page's button and the foot that answers a press are the screen's catalog's: one whole
// message each, with the alarm and the hour as its two arguments, so a language says them in its own
// order and with its own punctuation. The action is short and localized; only English and Spanish
// have translated feedback so far. The
// complete catalogs carry English feedback rather than missing entries, while short actions have
// equivalents. The rest of the screen languages fall back to English through Text, as
// every other line of the screen's own text does.
func TestSunriseSkipMessagesInEveryScreenLanguage(t *testing.T) {
	keys := []string{"sunrise_skip_action", "sunrise_skip_done", "sunrise_skip_refused"}
	actions := map[string]string{"": "Skip", "en": "Skip", "es": "Omitir", "de": "Überspringen", "fr": "Ignorer", "it": "Salta", "ja": "スキップ", "tr": "Atla", "nl": "Skip", "pt": "Skip", "ru": "Skip", "xx": "Skip"}
	for _, key := range keys {
		if uiEnglish[key] == "" {
			t.Fatalf("no English for %s", key)
		}
	}
	// screenLanguages are the languages the screen's setting offers, and English: "" is the language
	// the screen starts in, and "xx" one with no catalog of its own at all.
	screenLanguages := []string{"", "en", "es", "de", "fr", "it", "nl", "pt", "ru", "tr", "ja", "xx"}
	for _, lang := range screenLanguages {
		if got, want := Text(lang, "sunrise_skip_action"), actions[lang]; got != want {
			t.Errorf("%q: action says %q, want %q", lang, got, want)
		}
		for _, key := range keys {
			if got := Text(lang, key); got == "" || got == key {
				t.Errorf("%q: %s says %q", lang, key, got)
			}
		}
		// Both arguments are in the line, and nothing is left unformatted: a message that lost one of
		// them would put the alarm or the hour out of the line somebody reads.
		for _, key := range keys[1:] {
			line := fmt.Sprintf(Text(lang, key), "Wake up", "7:00 AM")
			if !strings.Contains(line, "Wake up") || !strings.Contains(line, "7:00 AM") || strings.Contains(line, "%") {
				t.Errorf("%q: %s says %q", lang, key, line)
			}
		}
	}
	// Feedback is translated in Spanish; other catalogs retain the maintainer-approved English
	// placeholders. Short action words are checked independently above.
	for _, key := range keys[1:] {
		if Text("es", key) == uiEnglish[key] {
			t.Errorf("es: %s is the English text %q", key, uiEnglish[key])
		}
		for _, lang := range screenLanguages {
			if lang == "es" {
				continue
			}
			if got := Text(lang, key); got != uiEnglish[key] {
				t.Errorf("%q: %s says %q, want the English %q until a native speaker writes it", lang, key, got, uiEnglish[key])
			}
		}
	}
	// Complete catalogs retain explicit feedback placeholders rather than missing keys; short
	// action equivalents were checked above.
	for _, lang := range completeCatalogs {
		for _, key := range keys[1:] {
			if got := uiTranslations[lang][key]; got != uiEnglish[key] {
				t.Errorf("%s: %s says %q, want the English %q until a native speaker writes it", lang, key, got, uiEnglish[key])
			}
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

// completeCatalogs are the screen languages translated in full. A key added to uiEnglish has to be
// added to each of them too, or that line shows in English on their screens.
var completeCatalogs = []string{"de", "fr", "it", "ja", "tr"}

func TestCompleteCatalogsHaveEveryKey(t *testing.T) {
	for _, lang := range completeCatalogs {
		catalog, ok := uiTranslations[lang]
		if !ok {
			t.Errorf("%s: no catalog", lang)
			continue
		}
		var missing []string
		for key := range uiEnglish {
			if catalog[key] == "" {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			t.Errorf("%s: %d keys not translated: %s", lang, len(missing), strings.Join(missing, " "))
		}
	}
}

// Turkish capitals: i is a dotted İ and ı a plain I; every other language as strings.ToUpper has it.
func TestTurkishCapitals(t *testing.T) {
	for _, c := range []struct{ lang, in, want string }{
		{"tr", "Pazartesi", "PAZARTESİ"}, {"tr", "Salı", "SALI"}, {"tr", "Ekim", "EKİM"},
		{"en", "Friday", "FRIDAY"}, {"", "pizza", "PIZZA"},
	} {
		if got := Upper(c.lang, c.in); got != c.want {
			t.Errorf("Upper(%q, %q) = %q, want %q", c.lang, c.in, got, c.want)
		}
	}
	if got := UpperRune("tr", 'i'); got != 'İ' {
		t.Errorf("UpperRune(tr, i) = %q, want İ", got)
	}
}
