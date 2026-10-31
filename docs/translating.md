# Translating the screen

The Show, the Spot and the Skylight can show their screen in another language: **Settings → General →
Screen language**. This page says how complete each language is, what stays in English on purpose,
and how to send a translation or a fix, without having to build anything.

## How far each language is

The counts come from the screen's catalog (`echod/internal/lib/locale/text.go`) and are checked by
CI, so they're always current. *Translated by* and *Tried on a device* are kept by hand: a language
can be complete and still never have been looked at on a real screen.

<!-- status:begin -->
| Language | Strings | Coverage | Translated by | Tried on a device |
|---|---:|---:|---|---|
| English | 665 | source | | |
| French (fr) | 665 | 100% | royto (#346) | Not yet |
| German (de) | 665 | 100% | christianschmidt (#322) | Echo Show 5 1st gen, v1.6.12 (#322) |
| Italian (it) | 665 | 100% | angelor87 (#310, #353) | Echo Show 5 1st gen (#353) |
| Japanese (ja) | 665 | 100% | lem0njuice0 (#270) | Echo Show 5 2nd gen (#270) |
| Spanish (es) | 289 | 43% | KOPYk4T (#327) | Echo Show 5 2nd gen, v1.6.12 (#327) |
| Turkish (tr) | 665 | 100% | senolsun (#349) | Not yet |
<!-- status:end -->

Dutch, Portuguese and Russian write the clock's day and date, the forecast and the weather's words;
the rest of their screen is English. They don't have a catalog of their own yet.

## What stays the same in every language

These are left as they are on purpose, so they aren't missing translations:

- **Names**: clock styles, themes, skins and fonts, like a font's name
  ([#324](https://github.com/HuskerMinion/techo5/issues/324)), and product names: Home Assistant,
  Music Assistant, Tailscale, NetBird, AirPlay, Sendspin, Spotify.
- **The camera test page**: a diagnostic page, kept in English
  ([#324](https://github.com/HuskerMinion/techo5/issues/324)).
- **The Wi-Fi password keyboard**: its keys type the password, so they're the same everywhere.
- **Error text** from the device's own checks (an address it can't use, a folder it can't open).
- **The local camera's name** ("This Show"): it's saved and matched by voice, so it stays as it is
  until the name and its label are split.

Not translated yet: the **Record takes** cards on the screen (new in v1.6.16), apart from their buttons.

## Sending a translation

You don't need to build or edit code. Open an issue (or comment on your language's issue if there is
one) with, for each string:

- the **key** (the left side in `text.go`, like `ui_glance_chip_size`), or the English text if you
  can't find the key,
- the **English** text,
- your **translation**,
- and where it shows, if it isn't obvious.

If you're comfortable with GitHub, a pull request that changes your language's block in `text.go`
is welcome too; `echod/internal/lib/locale/README.md` says how the catalog works, and its "Adding a
language" section lists everything a new language needs.

What not to change:

- the **keys**, and the English text in `uiEnglish`, which is the source,
- **placeholders** like `%s` and `%d`: keep each one, in the same order,
- settings values, names and anything in the list above.

## Keeping it short

Settings rows and pickers are narrow, on the Show 5 especially. Shorten first: a natural, shorter
wording that keeps the meaning beats a literal one that gets cut off with "…". If a row still doesn't
fit, say so in the issue, with a photo of the screen if you can, and that row can wrap or use smaller
text.

## Trying it on a device

Choose your language under **Screen language** and go through the settings, the alarm editor, the
calendar and a camera view. When you report back, say the device and generation, the TECHO5 version
(**Settings → General → Updates**) and the screen or setting, and add a photo where a label is cut off
or wraps oddly. Check photos and logs for anything personal before posting them. If you can't run a
test build yourself, that's fine: say so, and a release will carry your translation.
