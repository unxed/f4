Formatting: a language file is grouped by key namespace, not chronological.
Do not append new keys to the end of the file — put each one in the group its
first key component names, then run the formatter from the repository root:

    go run ./tools/langfmt -w internal/i18n/lang/*.lng
    go run ./tools/langfmt -check internal/i18n/lang/*.lng

CI runs the second form, so a file with a namespace split across two places
fails the build. See ../../../docs/I18N.md for the full rules.

How to test (in repo root):

GOMAXPROCS=1 go test -run="Test.*(Lang|Layout|Translation|Contamination|Bidi)" -v

`TestLangConsistency` measures translation coverage separately for every
language: a key counts only when it is present in that language and in the
English source file. The minimum covered-key count for each language is stored
in `coverage_baseline.txt`; the test fails if a language drops below its
baseline and reports the complete missing-key list with `-v`.

When a deliberate translation change establishes a new floor, regenerate the
counts and inspect the diff before committing it:

    F4_UPDATE_COVERAGE_BASELINE=1 go test -run '^TestLangConsistency$' ./internal/i18n

The command records the current coverage only; it does not translate missing
keys. Review the resulting `coverage_baseline.txt` and commit it together with
the language changes.

This command must stay green. Three cheap canaries guard the translation files
against machine translation damage:

  TestLanguageAlphabetsContamination
      a script the language may not use at all (Cyrillic inside he.lng).
  TestTranslationsAreFreeOfHomoglyphs
      a single word written in two scripts at once: a Hebrew word carrying an
      Arabic vav, a Belarusian word ending in a Latin "ka". Catches damage
      inside a script the language does use, which the check above cannot see.
  TestTranslationsHaveNoBidiControls
      an invisible LRM/RLM/isolate baked into a string. Ordering belongs to
      the renderer, not to the data.

All three cache their result and skip when nothing they depend on changed.
Set F4_FORCE_TESTS=1 (or CI=1) to run them unconditionally.

The homoglyph backlog is closed. The 39 damaged lines in help/*.hlf have been
repaired, lang/homoglyph_baseline.txt is gone, and both script canaries now
guard every file with no exceptions, lang/*.lng and help/*.hlf alike. The
loader in lang_homoglyphs_test.go still tolerates a missing baseline, so a
pass that ever uncovers a large batch of damage can re-create the file, work
it off and delete it again.

What is left of the localization audit:

  1. The "Tech Debt -> Missing key" list that TestLangConsistency prints.
     Those keys fall back to English at runtime, so they are the least urgent
     part. The longest lists are hi, hy, hu, zh and ar.
  2. A reading pass over the captions added on 2026-08-10. They came from a
     model and needed six hand fixes afterwards: a Polish verb in the middle
     of a Belarusian string, an Estonian button in fi.lng, a Portuguese noun
     in es.lng, a Belarusian conjunction in uk.lng, Konvertuj for Konwertuj
     in pl.lng, and a whole block duplicated into hi.lng. No canary can see
     any of that: they are real words, spelled correctly, in the wrong
     language. Only a speaker catches those.

Damage of the kind these canaries catch was introduced by translating with a
model and reviewing by eye. Any new translation pass should run the command
above before the commit, not after.
Two rules the caption pass of 2026-08-10 had to learn the hard way:

  A dialog title is written here with one leading space (" Attributes"), the
  way far writes it, but ParseIni trims the value and Painter.DrawTitle pads
  the title with a space on each side while drawing. So Msg("X.Title") never
  contains a space, and a test that compares a title must compare it against
  Msg(), never against a literal " X ". TestLangConsistency does enforce that
  the leading space is present in every translation, because it is what tells
  a translator the string is a title.

  A plugin under plugins/ is a separate test binary. package main does not
  link into it, so InitLang never runs and every vtui.Msg call returns "{Key}"
  unless vtui itself has a built-in for it. Any plugin whose dialogs are
  layout-tested has to load lang/en.lng on its own; plugins/netfox/lang_test.go
  is the pattern to copy, and its TestPluginCaptionsResolve also catches keys
  that exist in the source but never made it into en.lng.


Settings Center translations have an additional completeness check:
`go test ./internal/i18n -run TestSettingsTranslationsComplete`.
Every supported language must contain all `SettingsCenter.*` entries, including
option descriptions and choice explanations, with the same format substitutions
and intentional line breaks as English. When adding a Settings Center string,
update every language instead of relying on the English fallback.

The initial non-English/non-Russian Settings Center translations were produced
with machine translation, reused existing localized terminology where available,
and received terminology and structural checks. Native-speaker review remains
valuable for phrasing and technical nuance.
