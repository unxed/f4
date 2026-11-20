# Command line prompt format

Without a format string f4 draws `user@host:path$ ` and shortens the path when
the prompt grows past half the panel width. Options → Settings Center →
Terminal & environment → Command line prompt replaces that with a format string
of your own:

    UsePromptFormat = 1
    PromptFormat = $u@$n:$p$#

Both keys live in the `[Panel]` section of `settings.ini`, and the format
string is the one far2l uses (Options → Command line settings → "Set command
line prompt format"), so a string written for far2l can be pasted here as it
is.

## Codes

Codes are case-insensitive. A code f4 does not know expands to nothing, as in
far2l.

| Code    | Expands to                                                      |
|---------|-----------------------------------------------------------------|
| `$u`    | user name                                                        |
| `$n`    | host name                                                        |
| `$p`    | current path, with `~` for the home directory                    |
| `$r`    | current path, never abbreviated                                  |
| `$#`    | `#` for root, `$` for everyone else                              |
| `$t`    | current time, `HH:MM:SS`                                         |
| `$d`    | current date, `MM/DD/YY`                                         |
| `$z`    | current Git branch (empty outside a Git worktree)                  |
| `$h`    | erases the character before it                                   |
| `$@xx`  | for root only: the label `Root` (`Administrator` on Windows) between the two characters `xx`, e.g. `$@[]` gives `[Root]` |
| `$+`    | one `+` per entry on the folder stack                            |
| `$s`    | space                                                            |
| `$a`    | `&`                                                              |
| `$b`    | `\|`                                                             |
| `$c`    | `(`                                                              |
| `$f`    | `)`                                                              |
| `$g`    | `>`                                                              |
| `$l`    | `<`                                                              |
| `$q`    | `=`                                                              |
| `$$`    | `$`                                                              |

The punctuation codes exist because in cmd.exe a format string is also a
command line, where `&`, `|`, `<` and `>` cannot be typed directly. Anywhere
else the character itself works just as well.

## Examples

| Format              | Prompt                        |
|---------------------|-------------------------------|
| `$u@$n:$p$#` + space| `nz@nz-en:~/src/f4$ `         |
| `$p$#` + space      | `~/src/f4$ `                  |
| `[$t$h$h$h]$s$p$g`  | `[13:38] ~/src/f4>`           |
| `$@[]$s$r$g`        | `[Root] /etc>` when root      |

`$t$h$h$h` is the documented way to print the time without seconds: `$t`
writes `13:38:07` and each `$h` rubs out one character.

## What is not supported

- `$e`, `$v`, `$_` and `$m`. far2l does not implement these either.
- Environment variables in the format string. far2l expands them before it
  reads the codes, which makes `$HOSTNAME` work but also makes `$s` mean
  whatever the environment says `$s` means. `$u` and `$n` already name the
  user and the host.
- `$+` is read but always empty: f4 has no folder stack yet.

## Where the format does not apply

A panel showing a virtual filesystem keeps the built-in prompt. Its prompt
names the provider rather than the user and the host, and there is no local
home directory to abbreviate against, so `$u`, `$n` and `$p` would all describe
a machine the panel is not on.
