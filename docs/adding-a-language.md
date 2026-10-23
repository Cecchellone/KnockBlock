# Adding a language

KnockBlock shows text in two places: on the device display (firmware) and in
the Windows tray app (client). Adding a language needs no code changes, only
locale files. English is the fallback for anything missing.

Language codes are BCP 47 codes such as `de`, `fr` or `pt-BR`. The code is the
file name of each locale file.

## Device display (firmware)

1. Copy `firmware/locales/en.yaml` to `firmware/locales/<code>.yaml` and
   translate the values. Keep the keys: they are status IDs (`free`, `muted`,
   `mic`, `camera`, `call`, `dnd`, `offline`). Labels are shown in a large font on a
   320×172 display; keep them short (two lines at most, split at a space).
2. Add `<code>` to the `options` of the `display_language` select in
   `firmware/common/core.yaml`.
3. If the translation uses letters that are not in the display font yet, add
   them to `glyphs` of `font_label` in `firmware/knockblock.yaml`.
4. Run `esphome config firmware/knockblock.yaml`. It fails with a clear
   message if:
   - a key is not a known status ID,
   - the select options and the locale files do not match,
   - a character is missing from the font.

   A missing key only produces a warning; that label is shown in English.

The language is then selectable from Home Assistant or from the tray app
settings ("Device display language").

## Tray app (client)

1. Copy `client/locales/en.json` to `client/locales/<code>.json` and translate
   the values. Keep the keys and the `{placeholders}` exactly as they are.
2. Run `go test ./locales/` in `client/`. It fails if a key is missing or
   extra, or if a placeholder differs from English.

The app finds the new file by itself. It uses the language chosen in the
settings, otherwise the Windows display language (`it-IT` matches `it`),
otherwise English. A key missing from a translation falls back to English.

## Installer

1. Copy `installer/en-US.wxl` to `installer/<culture>.wxl` (e.g. `de-DE`),
   translate the strings and set `Language` to the language's LCID (e.g.
   1031). The standard setup pages come translated with WiX.
2. Add the culture and its LCID to `$languages` in `scripts/build-msi.ps1`.

The setup then shows that language when it is the Windows display language.
