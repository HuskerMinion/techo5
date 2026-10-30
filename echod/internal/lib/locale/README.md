# Screen localization

`Text(lang, key)` looks up a stable UI message key in `text.go`. The display's
`tr(key)` wrapper supplies the current screen language. Keep settings IDs, saved
option values and view identifiers separate from translated presentation text.

To add a language for these messages, add a map to `uiTranslations`. No drawing
changes are needed. A partial catalog falls back to `uiEnglish`; empty translations
also fall back. Unknown keys are returned verbatim. Keys are stable: changing the
English wording must not rename a key. Preserve formatting arguments such as `%d`.

Existing date, weather, phrase and update APIs remain available. UI text, phrases
and update dialogs share the same translation/fallback helper. Existing media
catalogs remain in place to avoid changing the behavior of other languages.

`go test ./internal/lib/locale` checks catalog keys, formatting arguments, English
fallbacks, Japanese dates and the keys used by all build-tagged display files.
Display tests cover language changes in cached settings images and calendar view
selection independently of translated labels.

Since v1.6.8, selecting Japanese as the screen language automatically downloads
a Noto Sans JP subset when needed. The download is skipped if an existing
user-added font is judged to support Japanese (using the glyph for `あ`). The
downloaded archive is verified against a pinned size and SHA-256. The font is
stored persistently and remains available after restarting. This localization
uses the existing `cjkfont` implementation without changing its download logic.
See `docs/setup.md` at the repository root for user-added fonts.

Localized pixels must include the language in their cache key (both the settings
base and its shared shell do this).

## Adding a language

A full screen language is a few small pieces, all in this folder and `lib/triggers`. Nothing is
downloaded for a language that draws with the screen's own fonts: a catalog adds about 25 KB to
echod.

1. **Translations.** Add a map for the language code to `uiTranslations` in `text.go`, with every key
   in `uiEnglish`, and its ten update strings in `update.go`. Keep the formatting arguments (`%d`,
   `%s`) as they are. A native speaker should write or check them.
2. **Dates and weather words.** Days, months, short forms, the date phrases and the sky words in
   `locale.go`, and the language's date order where it is not the default.
3. **Voice triggers.** The language's words for the weather, the rain map, going home and the camera
   in `lib/triggers/triggers.go`. A rain map word must also count as a weather word: the map is on
   the weather page.
4. **The language list.** Its own name for itself in the Screen language choices
   (`feature/display/triggers.go`).
5. **Complete catalogs.** Add the code to `completeCatalogs` in `text_test.go`. From then on a new
   English string fails the tests until it is translated here too.
6. **Capitals.** Headings and some dates are drawn in capitals with `locale.Upper`. If the language
   capitalizes a letter differently from `strings.ToUpper` (Turkish İ), add it there.
7. **Fonts.** The screen's own font (Go) draws Latin, Greek and Cyrillic, and the other fonts on offer
   draw Latin and fall back to it for the rest. A script it doesn't draw needs a font downloaded when
   the language is chosen, the way `feature/cjkfont` does it for Japanese. Right to
   left scripts and scripts that join their letters (Arabic, Hebrew, the Indic scripts) need text
   shaping the screen doesn't have yet.

8. **Status.** Add the language to `tools/locale/status.json` (its name, who translated it, where it
   was tried) and run `python3 tools/locale/status.py`, which updates the table in
   `docs/translating.md`. CI checks that table against the catalogs.

Run `go test ./internal/lib/locale ./internal/lib/triggers` with each build tag before the PR.
