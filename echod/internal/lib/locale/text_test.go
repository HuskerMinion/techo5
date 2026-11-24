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
	for _, lang := range []string{"", "en", "pt", "ru", "unknown"} {
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
var completeCatalogs = []string{"ca", "ca-valencia", "de", "es", "fr", "gl", "it", "ja", "pl", "ro", "tr"}

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

// The way out of one occurrence of an alarm, in the words the catalogs give it: the action, and the
// two answers with the moment as their one argument — never the alarm's name, which is the card's
// heading and has a limit of its own. A complete catalog without its own wording says the English one
// until it is translated, as the project does for new keys; the words are otherwise each catalog's
// own, so the loop below checks their shape, not the English text.
func TestSkipWords(t *testing.T) {
	formats := regexp.MustCompile(`%[+# .0-9\[\]-]*[a-zA-Z%]`)
	one := func(lang, key string) string {
		s := Text(lang, key)
		if s == "" {
			t.Errorf("%s/%s: empty", lang, key)
		} else if got := formats.FindAllString(s, -1); len(got) != 1 || got[0] != "%s" {
			t.Errorf("%s/%s: %q does not take the moment as its one argument", lang, key, s)
		}
		return s
	}
	if got := Text("es", "sunrise_skip_action"); got != "Omitir" {
		t.Errorf("es action is %q", got)
	}
	if got := one("es", "sunrise_skip_done"); got != "Omitida: la alarma de las %s" {
		t.Errorf("es done is %q", got)
	}
	if got := one("es", "sunrise_skip_refused"); got != "No se pudo omitir la alarma de las %s" {
		t.Errorf("es refused is %q", got)
	}
	check := func(lang string) {
		if action := Text(lang, "sunrise_skip_action"); action == "" {
			t.Errorf("%s: empty sunrise_skip_action", lang)
		} else if formats.MatchString(action) {
			t.Errorf("%s: action %q takes an argument", lang, action)
		}
		for _, key := range []string{"sunrise_skip_done", "sunrise_skip_refused"} {
			one(lang, key)
		}
	}
	for _, lang := range completeCatalogs {
		check(lang)
	}
	// The checks are on the shape, not on the English words, so a complete catalog that translates
	// these lines keeps passing: the day one does, this test does not hold it back.
	was := uiTranslations["de"]
	translated := make(map[string]string, len(was)+3)
	for key, value := range was {
		translated[key] = value
	}
	translated["sunrise_skip_action"] = "Überspringen"
	translated["sunrise_skip_done"] = "Übersprungen: der Alarm um %s"
	translated["sunrise_skip_refused"] = "Nicht übersprungen: der Alarm um %s"
	uiTranslations["de"] = translated
	t.Cleanup(func() { uiTranslations["de"] = was })
	check("de")
	if got := Text("de", "sunrise_skip_done"); got != "Übersprungen: der Alarm um %s" {
		t.Errorf("de done is %q", got)
	}
}

// Words that were once written into the drawing code: in every catalog, the partial ones too, so no
// screen shows them in English.
func TestOnceHardcodedWordsAreInEveryCatalog(t *testing.T) {
	keys := []string{"popup_started", "popup_in_a_minute", "popup_in_d_minutes", "popup_in_an_hour", "popup_at_s",
		"reminder_untitled", "hint_no_start", "hint_favorite", "hint_no_home", "hint_play_failed", "hint_dropping",
		"hint_unplayable", "call_muted", "silenced", "press_again_to_snooze_d_min"}
	for key := range uiEnglish {
		if strings.HasPrefix(key, "clock_style_") && key != "clock_style_label" && key != "clock_style_description" {
			keys = append(keys, key)
		}
	}
	for lang, catalog := range uiTranslations {
		for _, key := range keys {
			if catalog[key] == "" {
				t.Errorf("%s: no %s", lang, key)
			}
		}
	}
}

// First run's words on the screen (the firstrun_ keys): each one drawn is in uiEnglish, each one in
// uiEnglish is drawn, and every catalog, the partial ones too, has them all, so a screen in another
// language never walks somebody through setting it up in English.
func TestFirstRunWordsAreInEveryCatalog(t *testing.T) {
	drawn := map[string]bool{}
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
			if !ok || len(call.Args) != 1 {
				return true
			}
			if name, ok := call.Fun.(*ast.Ident); !ok || name.Name != "tr" {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok {
				if key, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(key, "firstrun_") {
					drawn[key] = true
				}
			}
			return true
		})
	}
	if len(drawn) == 0 {
		t.Fatal("no firstrun_ key is drawn")
	}
	var keys []string
	for key := range uiEnglish {
		if strings.HasPrefix(key, "firstrun_") {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for key := range drawn {
		if _, ok := uiEnglish[key]; !ok {
			t.Errorf("%s is drawn but not in uiEnglish", key)
		}
	}
	for _, key := range keys {
		if !drawn[key] {
			t.Errorf("%s is in uiEnglish but nothing draws it", key)
		}
	}
	for _, lang := range []string{"es", "fr", "gl", "it", "nl", "ja", "de", "tr"} {
		if _, ok := uiTranslations[lang]; !ok {
			t.Errorf("%s: no catalog", lang)
		}
	}
	for lang, catalog := range uiTranslations {
		for _, key := range keys {
			if catalog[key] == "" {
				t.Errorf("%s: no %s", lang, key)
			}
		}
	}
}

// A muted call is its microphone off, not the sound: French and Italian say so (ui_muted there reads
// as the sound off).
func TestAMutedCallSaysTheMicrophone(t *testing.T) {
	for _, lang := range []string{"fr", "it"} {
		if got := Text(lang, "call_muted"); !strings.HasPrefix(got, "Micro") || got == Text(lang, "ui_muted") {
			t.Errorf("%s: a muted call says %q", lang, got)
		}
	}
}
