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

Japanese rendering requires an installed font containing Japanese glyphs; see
the own-font instructions in `docs/setup.md` at the repository root. Language
selection does not install fonts. Localized pixels must include the language in
their cache key (both the settings base and its shared shell do this).
