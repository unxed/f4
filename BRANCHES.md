# Ветки Лунобота

Инвентарь веток и рабочих деревьев, проверенный 08-09-2026 по текущему `main`
(`a782f44766b4f7d96d4218ea5a45ea6274f4c7c5`), списку PR GitHub и локальному
`git worktree list`.

Статус `слита` означает, что соответствующий PR уже в `main`. Статус
`локальная/висячая` означает, что ветка или рабочее дерево ещё видны в общей
рабочей среде, но активного PR для них не найдено. Этот файл не является
разрешением удалять чужие ветки: в общей рабочей копии такие ветки сначала
проверяются с владельцем.

## Слитые ветки

| Ветка | Результат |
| --- | --- |
| `codex/242-registry-readonly` | PR #943, слита; remote ref ещё существует |
| `codex/242-status` | PR #946, слита; remote ref ещё существует |
| `codex/672-status` | PR #940, слита; remote ref ещё существует |
| `codex/885-status-detail` | PR #955, слита; remote ref ещё существует |
| `codex/885-status-part2` | PR #954, слита; remote ref ещё существует |
| `codex/89-status` | PR #952, слита; remote ref ещё существует |
| `codex/904-context-help-portable` | PR #928, слита; remote ref ещё существует |
| `codex/914-status` | PR #949, слита; remote ref ещё существует |
| `codex/914-status-ci-link` | PR #951, слита; remote ref ещё существует |
| `codex/lunobot-2-266-menu-mnemonic` | PR #931, слита; remote ref ещё существует |
| `codex/lunobot-2-881-terminal-selection` | PR #930, слита; remote ref ещё существует |
| `codex/lunobot-2-882-readme-order` | PR #937, слита; remote ref ещё существует |
| `codex/lunobot-2-882-status` | PR #938, слита; remote ref ещё существует |
| `codex/lunobot-2-882-termux-docs` | PR #936, слита; remote ref ещё существует |
| `codex/lunobot2-672-status` | PR #939, слита; remote ref ещё существует |
| `codex/lunobot2-607-status` | PR #958, слита; remote ref ещё существует |
| `codex/lunobot2-901-status` | PR #956, слита; remote ref ещё существует |
| `codex/branches-doc` | PR #959, слита; remote ref после merge не обнаружен |
| `codex/lunobot2-branches-fix` | PR #960, слита; remote ref ещё существует |
| `codex/lunobot-1-511-status` | PR #947, слита; remote ref ещё существует |
| `codex/lunobot-1-screen-dump` | PR #933, слита; remote ref ещё существует |
| `codex/lunobot-1-status-docs` | PR #953, слита; remote ref ещё существует |
| `codex/lunobot2-320-status` | PR #957, слита; remote ref после merge не обнаружен |
| `codex/lunobot2-368-status` | PR #944, слита; remote ref ещё существует |
| `codex/lunobot2-885-ci-status` | PR #950, слита; remote ref ещё существует |
| `codex/lunobot2-885-conpty` | PR #948, слита; локальное рабочее дерево ещё существует, remote ref удалён |
| `codex/lunobot2-885-status` | PR #945, слита; remote ref ещё существует |
| `codex/232-codepage-cycle` | PR #830, #841 и #844, слита; сохранилась локально |
| `codex/878-220-temp-panel` | PR #879, слита; сохранилась локально и в рабочем дереве |
| `codex/fix-492-right-ctrl-default-hotkeys` | PR #829, слита; сохранилась локально |
| `codex/fix-ci-unused-archive-test` | PR #842, слита; сохранилась локально и в remote-tracking |
| `codex/fix-clipboard-race` | PR #820, слита; сохранилась локально |
| `codex/issue-795` | PR #809, слита; сохранилась локально |

## Локальные или висячие ветки без найденного активного PR

| Ветка | Где обнаружена |
| --- | --- |
| `codex/815-f3-flate` | локальная ветка и рабочее дерево `/tmp/f4-815-f3`, prunable |
| `codex/815-queued-tar-copy` | локальная ветка и рабочее дерево `/tmp/f4-815`, prunable |
| `codex/816-encrypted-extract` | локальная ветка и рабочее дерево `/tmp/f4-issue-816`, prunable |
| `codex/816-publish-main` | локальная ветка и рабочее дерево `/tmp/f4-816-main`, prunable |
| `codex/818-htop` | локальная ветка |
| `codex/91-freebsd` | локальная ветка и рабочее дерево `/tmp/f4-issue-91`, prunable |
| `codex/comboupd` | локальная ветка |
| `codex/fix-836-bookmark-ctrl` | локальная ветка и рабочее дерево `/tmp/f4-issue-836`, prunable |
| `codex/fix-manual-update-ci` | локальная ветка и рабочее дерево `/tmp/f4-fix-manual-update-ci`, prunable |
| `codex/conpty-idea-c` | устаревший remote-tracking ref, активного PR не найдено |

## Правило обновления

После каждого коммита в `main` этот список проверяется вместе с `CI.md`.
Слитые ветки не считаются активной работой; локальные ветки в общей среде не
удаляются автоматически, пока не подтверждены их владелец и отсутствие
нужного незавершённого результата.
