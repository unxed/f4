# Project Structure

Every file tracked in the repository. Regenerate with
`scripts/filelist_update.sh` after adding or moving files.

    .
    ├── .agents
    │   ├── agents
    │   │   ├── best-practices-sidecar.md
    │   │   ├── commit-preparer.md
    │   │   ├── docs-auditor.md
    │   │   ├── implement-coordinator.md
    │   │   ├── implement-worker.md
    │   │   ├── loop-critic.md
    │   │   ├── loop-evaluator.md
    │   │   ├── loop-invariant-prep.md
    │   │   ├── loop-orchestrator.md
    │   │   ├── loop-perf-prep.md
    │   │   ├── loop-planner.md
    │   │   ├── loop-producer.md
    │   │   ├── loop-refiner.md
    │   │   ├── loop-test-prep.md
    │   │   ├── plan-coordinator.md
    │   │   ├── plan-polisher.md
    │   │   ├── review-sidecar.md
    │   │   ├── rules-sidecar.md
    │   │   └── security-sidecar.md
    │   └── skills
    │       ├── aif
    │       │   ├── references
    │       │   │   ├── config-template.yaml
    │       │   │   └── update-config.mjs
    │       │   └── SKILL.md
    │       ├── aif-architecture
    │       │   ├── references
    │       │   │   └── architecture.md
    │       │   └── SKILL.md
    │       ├── aif-archive
    │       │   └── SKILL.md
    │       ├── aif-best-practices
    │       │   └── SKILL.md
    │       ├── aif-build-automation
    │       │   ├── references
    │       │   │   ├── BEST-PRACTICES.md
    │       │   │   ├── DOC-INTEGRATION.md
    │       │   │   └── SUMMARY-FORMAT.md
    │       │   ├── SKILL.md
    │       │   └── templates
    │       │       ├── justfile-go
    │       │       ├── justfile-gradle
    │       │       ├── justfile-maven
    │       │       ├── justfile-node
    │       │       ├── justfile-php
    │       │       ├── justfile-python
    │       │       ├── justfile-ruby
    │       │       ├── justfile-rust
    │       │       ├── magefile-basic.go
    │       │       ├── magefile-full.go
    │       │       ├── makefile-go.mk
    │       │       ├── makefile-gradle.mk
    │       │       ├── makefile-maven.mk
    │       │       ├── makefile-node.mk
    │       │       ├── makefile-php.mk
    │       │       ├── makefile-python.mk
    │       │       ├── makefile-ruby.mk
    │       │       ├── makefile-rust.mk
    │       │       ├── taskfile-go.yml
    │       │       ├── taskfile-gradle.yml
    │       │       ├── taskfile-maven.yml
    │       │       ├── taskfile-node.yml
    │       │       ├── taskfile-php.yml
    │       │       ├── taskfile-python.yml
    │       │       ├── taskfile-ruby.yml
    │       │       └── taskfile-rust.yml
    │       ├── aif-ci
    │       │   ├── references
    │       │   │   ├── AUDIT-REPORT.md
    │       │   │   ├── BEST-PRACTICES.md
    │       │   │   ├── GITLAB-PATTERNS.md
    │       │   │   ├── SERVICE-CONTAINERS.md
    │       │   │   └── TOOL-COMMANDS.md
    │       │   ├── SKILL.md
    │       │   └── templates
    │       │       ├── github
    │       │       │   ├── go.yml
    │       │       │   ├── java.yml
    │       │       │   ├── node.yml
    │       │       │   ├── php.yml
    │       │       │   ├── python.yml
    │       │       │   └── rust.yml
    │       │       └── gitlab
    │       │           ├── go.yml
    │       │           ├── java.yml
    │       │           ├── node.yml
    │       │           ├── php.yml
    │       │           ├── python.yml
    │       │           └── rust.yml
    │       ├── aif-commit
    │       │   └── SKILL.md
    │       ├── aif-docs
    │       │   ├── references
    │       │   │   └── REVIEW-CHECKLISTS.md
    │       │   ├── SKILL.md
    │       │   └── templates
    │       │       └── html-template.html
    │       ├── aif-evolve
    │       │   └── SKILL.md
    │       ├── aif-explore
    │       │   ├── references
    │       │   │   └── ULTRA-RESEARCH-FORMAT.md
    │       │   └── SKILL.md
    │       ├── aif-fix
    │       │   └── SKILL.md
    │       ├── aif-grounded
    │       │   └── SKILL.md
    │       ├── aif-implement
    │       │   ├── references
    │       │   │   ├── IMPLEMENTATION-GUIDE.md
    │       │   │   └── LOGGING-GUIDE.md
    │       │   └── SKILL.md
    │       ├── aif-improve
    │       │   ├── references
    │       │   │   ├── CHECK-MODE.md
    │       │   │   ├── EXAMPLES.md
    │       │   │   ├── LIST-MODE.md
    │       │   │   └── VALIDATOR.md
    │       │   └── SKILL.md
    │       ├── aif-loop
    │       │   ├── references
    │       │   │   ├── ACTIVE-TIME-BUDGET.md
    │       │   │   ├── CONTEXT-MANAGEMENT.md
    │       │   │   ├── CRITERIA-TEMPLATES.md
    │       │   │   ├── PHASE-CONTRACTS.md
    │       │   │   ├── RULE-SCHEMA.md
    │       │   │   └── TERMINAL-REPORT.md
    │       │   └── SKILL.md
    │       ├── aif-plan
    │       │   ├── references
    │       │   │   ├── EXAMPLES.md
    │       │   │   ├── TASK-FORMAT.md
    │       │   │   └── ULTRA-FORMAT.md
    │       │   └── SKILL.md
    │       ├── aif-qa
    │       │   ├── references
    │       │   │   ├── CHANGE-SUMMARY.md
    │       │   │   ├── TEST-CASES.md
    │       │   │   └── TEST-PLAN.md
    │       │   ├── SKILL.md
    │       │   └── templates
    │       │       ├── CHANGE-SUMMARY.md
    │       │       ├── TEST-CASES.md
    │       │       └── TEST-PLAN.md
    │       ├── aif-qa-check
    │       │   ├── SKILL.md
    │       │   └── templates
    │       │       └── QA-CHECK.md
    │       ├── aif-review
    │       │   ├── references
    │       │   │   ├── CHECK-MODE.md
    │       │   │   ├── SEVERITY.md
    │       │   │   └── VALIDATOR.md
    │       │   └── SKILL.md
    │       ├── aif-roadmap
    │       │   └── SKILL.md
    │       ├── aif-rules
    │       │   └── SKILL.md
    │       ├── aif-rules-check
    │       │   ├── references
    │       │   │   └── RULES-CHECK-CONTRACT.md
    │       │   └── SKILL.md
    │       ├── aif-security-checklist
    │       │   ├── references
    │       │   │   ├── AUTH-PATTERNS.md
    │       │   │   ├── PROMPT-INJECTION.md
    │       │   │   └── RACE-CONDITIONS.md
    │       │   ├── scripts
    │       │   │   └── audit.sh
    │       │   └── SKILL.md
    │       ├── aif-skill-generator
    │       │   ├── references
    │       │   │   ├── BEST-PRACTICES.md
    │       │   │   ├── EXAMPLES.md
    │       │   │   ├── LEARN-MODE.md
    │       │   │   ├── SECURITY-SCANNING.md
    │       │   │   └── SPECIFICATION.md
    │       │   ├── scripts
    │       │   │   ├── cleanup-blocked-skill.py
    │       │   │   ├── search-skills.py
    │       │   │   ├── security-scan.py
    │       │   │   └── validate.sh
    │       │   ├── SKILL.md
    │       │   └── templates
    │       │       ├── basic.md
    │       │       ├── dynamic-context.md
    │       │       ├── research.md
    │       │       ├── task.md
    │       │       └── visual.md
    │       ├── aif-verify
    │       │   ├── references
    │       │   │   ├── CONTEXT-GATES-AND-OWNERSHIP.md
    │       │   │   └── GATE-RESULT-CONTRACT.md
    │       │   └── SKILL.md
    │       ├── golang-code-style
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   └── details.md
    │       │   └── SKILL.md
    │       ├── golang-concurrency
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── channels-and-select.md
    │       │   │   ├── pipelines.md
    │       │   │   └── sync-primitives.md
    │       │   └── SKILL.md
    │       ├── golang-context
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── cancellation.md
    │       │   │   ├── http-services.md
    │       │   │   └── values-tracing.md
    │       │   └── SKILL.md
    │       ├── golang-database
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── performance.md
    │       │   │   ├── scanning.md
    │       │   │   ├── testing.md
    │       │   │   └── transactions.md
    │       │   └── SKILL.md
    │       ├── golang-design-patterns
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── architecture.md
    │       │   │   ├── clean-architecture.md
    │       │   │   ├── data-handling.md
    │       │   │   ├── ddd.md
    │       │   │   ├── hexagonal-architecture.md
    │       │   │   └── resource-management.md
    │       │   └── SKILL.md
    │       ├── golang-documentation
    │       │   ├── assets
    │       │   │   └── templates
    │       │   │       ├── CHANGELOG.md
    │       │   │       ├── CONTRIBUTING.md
    │       │   │       ├── llms.txt
    │       │   │       └── README.md
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── application.md
    │       │   │   ├── code-comments.md
    │       │   │   ├── library.md
    │       │   │   └── project-docs.md
    │       │   └── SKILL.md
    │       ├── golang-error-handling
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── error-creation.md
    │       │   │   ├── error-handling.md
    │       │   │   └── error-wrapping.md
    │       │   └── SKILL.md
    │       ├── golang-naming
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── functions-methods.md
    │       │   │   ├── identifiers.md
    │       │   │   ├── packages-files.md
    │       │   │   ├── testing.md
    │       │   │   └── types-errors.md
    │       │   └── SKILL.md
    │       ├── golang-performance
    │       │   ├── assets
    │       │   │   └── prometheus-alerts.yml
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── caching.md
    │       │   │   ├── cpu.md
    │       │   │   ├── io-networking.md
    │       │   │   ├── memory.md
    │       │   │   ├── observability.md
    │       │   │   └── runtime.md
    │       │   └── SKILL.md
    │       ├── golang-security
    │       │   ├── evals
    │       │   │   └── evals.json
    │       │   ├── references
    │       │   │   ├── architecture.md
    │       │   │   ├── checklist.md
    │       │   │   ├── cookies.md
    │       │   │   ├── cryptography.md
    │       │   │   ├── filesystem.md
    │       │   │   ├── injection.md
    │       │   │   ├── logging.md
    │       │   │   ├── memory-safety.md
    │       │   │   ├── network.md
    │       │   │   ├── secrets.md
    │       │   │   ├── third-party.md
    │       │   │   └── threat-modeling.md
    │       │   └── SKILL.md
    │       └── golang-testing
    │           ├── evals
    │           │   └── evals.json
    │           ├── references
    │           │   ├── benchmarks.md
    │           │   ├── coverage.md
    │           │   ├── examples.md
    │           │   ├── helpers.md
    │           │   ├── http-testing.md
    │           │   ├── integration-testing.md
    │           │   └── mocking.md
    │           └── SKILL.md
    ├── AGENTS.md
    ├── .ai-factory
    │   ├── ARCHITECTURE.md
    │   ├── config.yaml
    │   ├── DESCRIPTION.md
    │   └── rules
    │       └── base.md
    ├── .ai-factory.json
    ├── artifacts
    │   └── README.md
    ├── cmd
    │   ├── f4
    │   │   ├── android_deps_test.go
    │   │   ├── architecture_test.go
    │   │   ├── cloudfox_deps_test.go
    │   │   ├── command_palette_coverage_test.go
    │   │   ├── frame_manager_capture_test.go
    │   │   ├── go2xp_shim.go
    │   │   ├── hardcoded_strings_test.go
    │   │   ├── ios_deps_test.go
    │   │   ├── lite_deps_test.go
    │   │   ├── main.go
    │   │   ├── rsrc_windows_amd64.syso
    │   │   ├── rsrc_windows_arm64.syso
    │   │   └── winescape_gate_test.go
    │   └── f4-gui-launcher
    │       ├── main_other.go
    │       ├── main_windows.go
    │       ├── main_windows_test.go
    │       └── testdata
    │           └── stub
    │               └── main.go
    ├── docs
    │   ├── ARCHIVE_DEPENDENCIES.md
    │   ├── BOOKMARKS.md
    │   ├── COLORS.md
    │   ├── CONPTY_GATE_REQUIREMENTS.md
    │   ├── CONPTY_GATE_STATUS.md
    │   ├── CONPTY_NATIVE_AGENT.md
    │   ├── CONPTY_NATIVE_AUDIT.md
    │   ├── CONPTY_NATIVE_PROBE.md
    │   ├── CONPTY_NATIVE_TEST.md
    │   ├── CONSOLE_MODES.md
    │   ├── CURSOR.md
    │   ├── DOCKER.md
    │   ├── DOTNET.md
    │   ├── DRAGDROP.md
    │   ├── FAR2L_DND.md
    │   ├── FFI.md
    │   ├── FISH+.md
    │   ├── FISH_PLUS_S2S.md
    │   ├── FUSE.md
    │   ├── HIGHLIGHTING.md
    │   ├── HIGHLIGHT.md
    │   ├── I18N.md
    │   ├── ISSUES
    │   │   ├── CI_WINDOWS_DESKTOP_EOL.md
    │   │   ├── ISSUE_1400_PANEL_MODES.md
    │   │   ├── ISSUE_1716_SFTP_SAVE_OVERWRITE.md
    │   │   ├── ISSUE_1721_EXTERNAL_EDITOR_EPERM.md
    │   │   ├── ISSUE_722_COPY_ACCESS_RIGHTS.md
    │   │   └── ISSUE_722_COPY_OPTIONS.md
    │   ├── KEYMAP.md
    │   ├── KUBERNETES.md
    │   ├── L10N_REPORT_GUIDE.md
    │   ├── LUA.md
    │   ├── MACKEYS.md
    │   ├── MACROS.md
    │   ├── MERMAID.md
    │   ├── MONGODB.md
    │   ├── NESTED_ARCHIVES.md
    │   ├── NETFOX.md
    │   ├── OPENWRT.md
    │   ├── PDF.md
    │   ├── PINNED_CONSOLE.md
    │   ├── PINNED_HOST_FACTS.md
    │   ├── PLAYER.md
    │   ├── PLUGINS.md
    │   ├── PLUGRING.md
    │   ├── PORTABILITY_BSD.md
    │   ├── PROMPT.md
    │   ├── REVIEW.md
    │   ├── SERVICES.md
    │   ├── settings-center-fields.json
    │   ├── SETTINGS_CENTER.md
    │   ├── SPREADSHEET.md
    │   ├── SYNC_DIRS.md
    │   ├── TERMINAL_JUNK_LOG.md
    │   ├── TERMINAL.md
    │   ├── TTYX.md
    │   ├── UPDATER.md
    │   ├── UPSTREAM.md
    │   ├── USER_MENU.md
    │   ├── UX_GUIDELINES.md
    │   ├── VFS.md
    │   ├── VIDEO.md
    │   ├── VTML.md
    │   ├── VTVIBE.md
    │   ├── WINCON_805_HANDOVER.md
    │   ├── WINCON.md
    │   └── WINE.md
    ├── embedded.go
    ├── f4.example.ini
    ├── flake.lock
    ├── flake.nix
    ├── .gitattributes
    ├── .github
    │   ├── actions
    │   │   ├── affected-packages
    │   │   │   └── action.yml
    │   │   └── shard-packages
    │   │       └── action.yml
    │   ├── assets
    │   │   └── screenshot.png
    │   ├── codecov.yml
    │   └── workflows
    │       ├── build.yml
    │       ├── go-cache-salt
    │       ├── openwrt.yml
    │       ├── quick.yml
    │       └── sandbox.yml
    ├── .gitignore
    ├── .golangci-strict.yml
    ├── .golangci.yml
    ├── go.mod
    ├── go.sum
    ├── highlight.ini
    ├── internal
    │   ├── action
    │   │   ├── order.go
    │   │   ├── registry.go
    │   │   └── registry_order_test.go
    │   ├── app
    │   │   ├── action_copyname_parent_test.go
    │   │   ├── action_copy_window_title_test.go
    │   │   ├── action_delete_cursor_test.go
    │   │   ├── action_enabled_test.go
    │   │   ├── action_labelkeys_test.go
    │   │   ├── action_marked_clipboard_test.go
    │   │   ├── action_menu.go
    │   │   ├── action_menu_test.go
    │   │   ├── action_menu_visibility_test.go
    │   │   ├── action_registry_test.go
    │   │   ├── action_restore_selection_test.go
    │   │   ├── actions_coverage_batch15_test.go
    │   │   ├── actions_coverage_batch17_test.go
    │   │   ├── actions_coverage_batch19_test.go
    │   │   ├── actions_coverage_batch24_test.go
    │   │   ├── actions_coverage_batch26_test.go
    │   │   ├── actions_coverage_helpers_test.go
    │   │   ├── actions_framework.go
    │   │   ├── actions_framework_test.go
    │   │   ├── actions.go
    │   │   ├── action_shortcut_conflict_test.go
    │   │   ├── actions_low_coverage_extra_test.go
    │   │   ├── actions_table.go
    │   │   ├── actions_table_order_test.go
    │   │   ├── actions_test.go
    │   │   ├── actions_view_by_type_test.go
    │   │   ├── ai_chat_panel_coverage_test.go
    │   │   ├── ai_chat_panel.go
    │   │   ├── ai_chat_panel_test.go
    │   │   ├── android_plugin_menu.go
    │   │   ├── android_plugin_menu_test.go
    │   │   ├── api.go
    │   │   ├── api_test.go
    │   │   ├── apply_command_test.go
    │   │   ├── arkanoid_coverage_test.go
    │   │   ├── arkanoid.go
    │   │   ├── arkanoid_test.go
    │   │   ├── attributes_test.go
    │   │   ├── autosave_settings_test.go
    │   │   ├── background_jobs_window.go
    │   │   ├── background_jobs_window_test.go
    │   │   ├── bom_test.go
    │   │   ├── bookmarks_dialog_test.go
    │   │   ├── bookmarks_test.go
    │   │   ├── bootstrap_backend.go
    │   │   ├── bootstrap_backend_test.go
    │   │   ├── bootstrap_coverage_batch34_test.go
    │   │   ├── bootstrap_coverage_batch37_test.go
    │   │   ├── bootstrap_coverage_batch42_test.go
    │   │   ├── bootstrap_coverage_batch43_test.go
    │   │   ├── bootstrap_coverage_batch45_test.go
    │   │   ├── bootstrap_coverage_batch46_test.go
    │   │   ├── bootstrap_coverage_batch47_test.go
    │   │   ├── bootstrap_coverage_extra_test.go
    │   │   ├── bootstrap_coverage_test.go
    │   │   ├── bootstrap_ctrlhandler_other.go
    │   │   ├── bootstrap_ctrlhandler_windows.go
    │   │   ├── bootstrap_ctrlhandler_windows_test.go
    │   │   ├── bootstrap_detach_unix.go
    │   │   ├── bootstrap_detach_unix_test.go
    │   │   ├── bootstrap_detach_windows.go
    │   │   ├── bootstrap_detach_windows_test.go
    │   │   ├── bootstrap.go
    │   │   ├── bootstrap_gui_test.go
    │   │   ├── bootstrap_nestedinput_other.go
    │   │   ├── bootstrap_nestedinput_test.go
    │   │   ├── bootstrap_nestedinput_windows.go
    │   │   ├── bootstrap_nestedinput_windows_test.go
    │   │   ├── bootstrap_session_test.go
    │   │   ├── bootstrap_settings.go
    │   │   ├── bootstrap_settings_test.go
    │   │   ├── bootstrap_startupdir_terminal_test.go
    │   │   ├── bootstrap_startupdir_test.go
    │   │   ├── bootstrap_startupfile_terminal_test.go
    │   │   ├── bootstrap_sudo_test.go
    │   │   ├── bootstrap_tty_test.go
    │   │   ├── bootstrap_unicode_test.go
    │   │   ├── calculator_ui.go
    │   │   ├── calculator_ui_test.go
    │   │   ├── calendar_ui.go
    │   │   ├── calendar_ui_test.go
    │   │   ├── child_env_test.go
    │   │   ├── child_env_universal_linux_test.go
    │   │   ├── clipboard_image_test.go
    │   │   ├── cloud_storage_lite.go
    │   │   ├── cloud_storage_lite_test.go
    │   │   ├── codepage_issue875_sticky_test.go
    │   │   ├── codepage_issue875_test.go
    │   │   ├── colorer_coverage_more_test.go
    │   │   ├── colorer_download_test.go
    │   │   ├── colorer_settings.go
    │   │   ├── colorer_settings_test.go
    │   │   ├── colorer_type_settings_coverage_test.go
    │   │   ├── colorer_type_settings.go
    │   │   ├── colorstyle_firstrun.go
    │   │   ├── colorstyle_firstrun_test.go
    │   │   ├── command_history_paths_test.go
    │   │   ├── command_palette_direct_frames.go
    │   │   ├── command_palette_direct_frames_test.go
    │   │   ├── command_palette_direct_panels_test.go
    │   │   ├── command_palette_drives.go
    │   │   ├── command_palette_drives_test.go
    │   │   ├── command_palette_dynamic_test.go
    │   │   ├── command_palette_frames.go
    │   │   ├── command_palette.go
    │   │   ├── command_palette_help.go
    │   │   ├── command_palette_help_test.go
    │   │   ├── command_palette_i18n.go
    │   │   ├── command_palette_i18n_test.go
    │   │   ├── command_palette_macros.go
    │   │   ├── command_palette_menu_leaves_test.go
    │   │   ├── command_palette_menu_test.go
    │   │   ├── command_palette_modal.go
    │   │   ├── command_palette_panels.go
    │   │   ├── command_palette_prefixes.go
    │   │   ├── command_palette_search.go
    │   │   ├── command_palette_search_test.go
    │   │   ├── command_palette_test.go
    │   │   ├── command_palette_ui.go
    │   │   ├── command_palette_ui_test.go
    │   │   ├── command_palette_workspace.go
    │   │   ├── command_prefix_registry_test.go
    │   │   ├── compare_content_ui.go
    │   │   ├── compare_content_ui_test.go
    │   │   ├── compare_folders_ui_coverage_test.go
    │   │   ├── compare_folders_ui.go
    │   │   ├── config_test.go
    │   │   ├── console_passthrough_test.go
    │   │   ├── copy_dialog_options_coverage_test.go
    │   │   ├── copy_dialog_options.go
    │   │   ├── copy_dialog_options_more_coverage_test.go
    │   │   ├── coverage_batch35_test.go
    │   │   ├── coverage_helpers_test.go
    │   │   ├── ctrl_row_labelkeys_test.go
    │   │   ├── debug.go
    │   │   ├── debug_hangdump_unix.go
    │   │   ├── debug_hangdump_windows.go
    │   │   ├── debug_log_test.go
    │   │   ├── delete_trash_test.go
    │   │   ├── diagnostics.go
    │   │   ├── diagnostics_test.go
    │   │   ├── dialog_copy_layout_test.go
    │   │   ├── dialog_copy_resize_test.go
    │   │   ├── dialog_layout_languages_test.go
    │   │   ├── dialog_layouts_test.go
    │   │   ├── dialog_outer_border.go
    │   │   ├── dialog_outer_border_test.go
    │   │   ├── disasm_editor_test.go
    │   │   ├── dotnet_info.go
    │   │   ├── dotnet_info_test.go
    │   │   ├── dragdrop_test.go
    │   │   ├── drive_bookmarks_test.go
    │   │   ├── drive_menu_options_test.go
    │   │   ├── editor_actions_test.go
    │   │   ├── editor_binary_open_test.go
    │   │   ├── editor_events.go
    │   │   ├── editor_host_test.go
    │   │   ├── editor_hotkeys_test.go
    │   │   ├── editor_keys_test.go
    │   │   ├── editor_save_wait_test.go
    │   │   ├── editor_test_helpers_test.go
    │   │   ├── f4_commands.go
    │   │   ├── f4_commands_test.go
    │   │   ├── farmenu_file_test.go
    │   │   ├── fast_find_overlay_test.go
    │   │   ├── file_associations_dispatch_test.go
    │   │   ├── file_associations_test.go
    │   │   ├── file_ops_panel_test.go
    │   │   ├── file_panel_sorting_regression_test.go
    │   │   ├── find_file.go
    │   │   ├── find_file_test.go
    │   │   ├── fish_server.go
    │   │   ├── fish_server_test.go
    │   │   ├── fkeys_hidden_panels_test.go
    │   │   ├── folder_history_actions_test.go
    │   │   ├── folder_history_navigation_test.go
    │   │   ├── folder_history_panel_test.go
    │   │   ├── framewatch.go
    │   │   ├── framewatch_vtui.go
    │   │   ├── git_actions.go
    │   │   ├── grabber.go
    │   │   ├── grabber_mouse_test.go
    │   │   ├── grabber_test.go
    │   │   ├── gui_font_combo_dialog_test.go
    │   │   ├── help_host_test.go
    │   │   ├── help_keys_ar_test.go
    │   │   ├── help_keys_he_test.go
    │   │   ├── help_keys_ru_test.go
    │   │   ├── help_keys_test.go
    │   │   ├── help_keys_tr_test.go
    │   │   ├── help_topics.go
    │   │   ├── history_bridge_coverage_test.go
    │   │   ├── history_bridge.go
    │   │   ├── history_dialog.go
    │   │   ├── history_dialog_test.go
    │   │   ├── history_hint_test.go
    │   │   ├── history_provider_test.go
    │   │   ├── hotkeys_ui.go
    │   │   ├── hotkeys_ui_test.go
    │   │   ├── image_gallery_panel_test.go
    │   │   ├── image_view_panel_test.go
    │   │   ├── ios_plugin_menu.go
    │   │   ├── ios_plugin_menu_test.go
    │   │   ├── issue1218_keybar_test.go
    │   │   ├── issue1229_test.go
    │   │   ├── issue1233_test.go
    │   │   ├── issue1237_test.go
    │   │   ├── issue1239_test.go
    │   │   ├── issue1268_test.go
    │   │   ├── issue408_reveal_test.go
    │   │   ├── issue413_test.go
    │   │   ├── issue54_test.go
    │   │   ├── issue561_test.go
    │   │   ├── issue631_test.go
    │   │   ├── issue821_test.go
    │   │   ├── issue856_mouse_capture_test.go
    │   │   ├── issue95_followup_test.go
    │   │   ├── keybar_injected_test.go
    │   │   ├── keymap_host_test.go
    │   │   ├── keymap_suspend.go
    │   │   ├── lang.go
    │   │   ├── lang_host_test.go
    │   │   ├── lite_build.go
    │   │   ├── lite_build_lite.go
    │   │   ├── local_language_files_test.go
    │   │   ├── macro_ctrlletter_test.go
    │   │   ├── macro_dispatch.go
    │   │   ├── macro_host.go
    │   │   ├── macro_host_test.go
    │   │   ├── macro_menu_items.go
    │   │   ├── macro_menu_items_test.go
    │   │   ├── macro_plugin_calls.go
    │   │   ├── macro_reload_action_test.go
    │   │   ├── macro_test.go
    │   │   ├── main_menu_bar_keys_test.go
    │   │   ├── main_menu_dropdown_test.go
    │   │   ├── main_test.go
    │   │   ├── managed_execution_test.go
    │   │   ├── markdown_view.go
    │   │   ├── markdown_view_test.go
    │   │   ├── media_app.go
    │   │   ├── menu_history_action.go
    │   │   ├── menu_history_test.go
    │   │   ├── menu_hotkeys_test.go
    │   │   ├── mock_failing_vfs_test.go
    │   │   ├── navigation_mode_test.go
    │   │   ├── netbrowse_actions.go
    │   │   ├── panel_actions_test.go
    │   │   ├── panel_menu_test.go
    │   │   ├── panel_plugin_keys_test.go
    │   │   ├── panel_plugins_test.go
    │   │   ├── panels_app_commands_coverage_test.go
    │   │   ├── panels_app_commands.go
    │   │   ├── panels_frame_app_test.go
    │   │   ├── panels_frame_drivecursor_windows_test.go
    │   │   ├── path_hints_test.go
    │   │   ├── path_identity_history_test.go
    │   │   ├── pdf_text.go
    │   │   ├── pdf_text_test.go
    │   │   ├── player_panel_actions_test.go
    │   │   ├── plughost_app.go
    │   │   ├── plugin_contributions.go
    │   │   ├── plugin_contributions_test.go
    │   │   ├── plugin_hotkeys_test.go
    │   │   ├── plugring_install_load_test.go
    │   │   ├── plugring_policy_test.go
    │   │   ├── plugring_rows_test.go
    │   │   ├── plugring_test.go
    │   │   ├── plugring_ui.go
    │   │   ├── plugring_ui_test.go
    │   │   ├── portable_paths_test.go
    │   │   ├── portable_test.go
    │   │   ├── process_environment_host.go
    │   │   ├── process_environment_host_test.go
    │   │   ├── proclist_actions.go
    │   │   ├── procname_linux.go
    │   │   ├── procname_linux_test.go
    │   │   ├── procname_other.go
    │   │   ├── pty_windows_panel_test.go
    │   │   ├── quickview_api.go
    │   │   ├── quit_dialog_keys_test.go
    │   │   ├── release_version_symbol_test.go
    │   │   ├── rpc_commands_test.go
    │   │   ├── runner_unix_panel_test.go
    │   │   ├── search_history_test.go
    │   │   ├── semantic_actions_test.go
    │   │   ├── semantic.go
    │   │   ├── semantic_test.go
    │   │   ├── settings_coverage_batch16_test.go
    │   │   ├── settings_host_coverage_batch49_test.go
    │   │   ├── settings_host_coverage_test.go
    │   │   ├── settings_host.go
    │   │   ├── settings_host_runtime_coverage_batch50_test.go
    │   │   ├── settings_host_test.go
    │   │   ├── settings_routes.go
    │   │   ├── settings_save_coverage_batch48_test.go
    │   │   ├── settings_save.go
    │   │   ├── settings_save_route_test.go
    │   │   ├── share_dialog_coverage_test.go
    │   │   ├── share_dialog.go
    │   │   ├── share_dialog_test.go
    │   │   ├── sheet_actions_coverage_test.go
    │   │   ├── sheet_actions.go
    │   │   ├── sheet_actions_test.go
    │   │   ├── sheet_compare_coverage_test.go
    │   │   ├── sheet_dialogs_coverage_test.go
    │   │   ├── sheet_dialogs.go
    │   │   ├── sheet_frame_coverage_extra_test.go
    │   │   ├── sheet_frame.go
    │   │   ├── sheet_frame_test.go
    │   │   ├── sheet_palette.go
    │   │   ├── sheet_palette_test.go
    │   │   ├── shell_integration_test.go
    │   │   ├── shutdown_cancel_test.go
    │   │   ├── simple_exec_test.go
    │   │   ├── sort_groups_test.go
    │   │   ├── sqlite_actions.go
    │   │   ├── sqlite_actions_test.go
    │   │   ├── startup_coverage_batch18_test.go
    │   │   ├── static_direct_actions.go
    │   │   ├── static_direct_actions_test.go
    │   │   ├── svcmgr_actions.go
    │   │   ├── sync_dirs_coverage_test.go
    │   │   ├── sync_dirs_ui.go
    │   │   ├── temp_panel_test.go
    │   │   ├── term_app_coverage_batch32_test.go
    │   │   ├── term_app.go
    │   │   ├── term_app_other.go
    │   │   ├── term_app_windows.go
    │   │   ├── terminal_dnd_bind.go
    │   │   ├── terminal_workspace_test.go
    │   │   ├── testdata
    │   │   │   └── action_order.golden
    │   │   ├── text_editor_bridge_test.go
    │   │   ├── theme_host_test.go
    │   │   ├── title.go
    │   │   ├── title_test.go
    │   │   ├── title_unix.go
    │   │   ├── title_windows.go
    │   │   ├── translator_test.go
    │   │   ├── updater.go
    │   │   ├── updater_issue635_test.go
    │   │   ├── updater_repro_lock_other_test.go
    │   │   ├── updater_repro_lock_windows_test.go
    │   │   ├── updater_repro_test.go
    │   │   ├── updater_test.go
    │   │   ├── user_menu_ini_test.go
    │   │   ├── user_menu_subst_test.go
    │   │   ├── viewer_app.go
    │   │   ├── viewer_editor_history_test.go
    │   │   ├── viewer_keys_test.go
    │   │   ├── vtvibe_ap_coverage_test.go
    │   │   ├── vtvibe_ap.go
    │   │   ├── vtvibe_ap_reject.go
    │   │   ├── vtvibe_ap_review.go
    │   │   ├── vtvibe_ap_review_test.go
    │   │   ├── vtvibe_ap_test.go
    │   │   ├── vtvibe_ap_undo.go
    │   │   ├── vtvibe_ap_undo_test.go
    │   │   ├── vtvibe_host_coverage_test.go
    │   │   ├── vtvibe_host_extra_coverage_test.go
    │   │   ├── vtvibe_host.go
    │   │   ├── vtvibe_host_test.go
    │   │   ├── win32_backend_test.go
    │   │   ├── window_toggle.go
    │   │   ├── window_toggle_other.go
    │   │   ├── window_toggle_test.go
    │   │   ├── window_toggle_windows.go
    │   │   ├── workspace_routing_test.go
    │   │   ├── workspace_session_test.go
    │   │   ├── worktree_identity_coverage_test.go
    │   │   ├── worktree_identity.go
    │   │   └── worktree_identity_test.go
    │   ├── appcmd
    │   │   └── commands.go
    │   ├── cmdline
    │   │   ├── apply_batch.go
    │   │   ├── apply_batch_test.go
    │   │   ├── apply_output.go
    │   │   ├── apply_output_test.go
    │   │   ├── apply_resources.go
    │   │   ├── apply_resources_test.go
    │   │   ├── apply_shortname_other.go
    │   │   ├── apply_shortname_windows.go
    │   │   ├── apply_shortname_windows_test.go
    │   │   ├── apply_subst.go
    │   │   ├── apply_subst_test.go
    │   │   ├── apply_transcript.go
    │   │   ├── filename_template.go
    │   │   ├── filename_template_test.go
    │   │   ├── line.go
    │   │   ├── line_semantic.go
    │   │   ├── line_semantic_test.go
    │   │   ├── line_test.go
    │   │   ├── prompt.go
    │   │   ├── prompt_test.go
    │   │   ├── prompt_unix.go
    │   │   ├── prompt_windows.go
    │   │   ├── quotes.go
    │   │   ├── quotes_test.go
    │   │   ├── quoting.go
    │   │   ├── quoting_test.go
    │   │   ├── resolve_other.go
    │   │   ├── resolve_windows.go
    │   │   └── resolve_windows_test.go
    │   ├── colorer
    │   │   ├── configs
    │   │   │   └── base
    │   │   │       └── hrd
    │   │   │           └── rgb
    │   │   │               └── radiola.hrd
    │   │   └── embedded.go
    │   ├── config
    │   │   ├── appearance_settings_test.go
    │   │   ├── atomic.go
    │   │   ├── atomic_test.go
    │   │   ├── clipboard_image_test.go
    │   │   ├── colorstyle_configured_test.go
    │   │   ├── config_glyph_style_test.go
    │   │   ├── config.go
    │   │   ├── config_test.go
    │   │   ├── cursor_style_test.go
    │   │   ├── fallback_language_test.go
    │   │   ├── grouping.go
    │   │   ├── grouping_test.go
    │   │   ├── options.go
    │   │   ├── options_test.go
    │   │   ├── overlay.go
    │   │   ├── overlay_test.go
    │   │   ├── proxy_settings_test.go
    │   │   ├── schema.go
    │   │   ├── serialize_roundtrip_test.go
    │   │   └── settings.go
    │   ├── dialog
    │   │   ├── about.go
    │   │   ├── about_os_other.go
    │   │   ├── about_os_unix.go
    │   │   ├── about_os_windows.go
    │   │   ├── about_test.go
    │   │   ├── attributes_created_time_test.go
    │   │   ├── attributes.go
    │   │   ├── attributes_mixed_test.go
    │   │   ├── attributes_mtime_validation_test.go
    │   │   ├── attributes_recursive_test.go
    │   │   ├── attributes_unix.go
    │   │   ├── attributes_windows.go
    │   │   ├── attributes_windows_test.go
    │   │   ├── caption.go
    │   │   ├── config_editor_coverage_batch36_test.go
    │   │   ├── config_editor.go
    │   │   ├── config_editor_test.go
    │   │   ├── envman_help_test.go
    │   │   ├── file.go
    │   │   ├── file_layout_test.go
    │   │   ├── file_resize_test.go
    │   │   ├── file_test.go
    │   │   ├── goto.go
    │   │   ├── goto_test.go
    │   │   ├── help
    │   │   │   ├── ar.hlf
    │   │   │   ├── be.hlf
    │   │   │   ├── bn.hlf
    │   │   │   ├── cs.hlf
    │   │   │   ├── de.hlf
    │   │   │   ├── en.hlf
    │   │   │   ├── es.hlf
    │   │   │   ├── et.hlf
    │   │   │   ├── fi.hlf
    │   │   │   ├── he.hlf
    │   │   │   ├── hi.hlf
    │   │   │   ├── hu.hlf
    │   │   │   ├── hy.hlf
    │   │   │   ├── ja.hlf
    │   │   │   ├── ka.hlf
    │   │   │   ├── ko.hlf
    │   │   │   ├── lt.hlf
    │   │   │   ├── lv.hlf
    │   │   │   ├── pl.hlf
    │   │   │   ├── README.md
    │   │   │   ├── ru.hlf
    │   │   │   ├── tr.hlf
    │   │   │   ├── uk.hlf
    │   │   │   └── zh.hlf
    │   │   ├── help.go
    │   │   ├── help_lang_test.go
    │   │   ├── help_languages.go
    │   │   ├── help_search.go
    │   │   ├── help_search_test.go
    │   │   ├── help_test.go
    │   │   ├── help_zoom_rewrap_test.go
    │   │   ├── hotkey_capture.go
    │   │   ├── hotkey_capture_test.go
    │   │   ├── label.go
    │   │   ├── main_test.go
    │   │   ├── path.go
    │   │   ├── profile_transfer_test.go
    │   │   ├── settings_codepage.go
    │   │   ├── settings_portable.go
    │   │   ├── settings_portable_test.go
    │   │   ├── settings_proxy_coverage_test.go
    │   │   ├── settings_proxy.go
    │   │   └── settings_proxy_test.go
    │   ├── diffview
    │   │   ├── diffview.go
    │   │   └── diffview_test.go
    │   ├── dirwatch
    │   │   ├── dirwatch.go
    │   │   ├── dirwatch_test.go
    │   │   ├── native_kqueue.go
    │   │   ├── native_linux.go
    │   │   ├── native_other.go
    │   │   ├── native_windows.go
    │   │   └── poll.go
    │   ├── dotnet
    │   │   ├── attrargs.go
    │   │   ├── attrargs_test.go
    │   │   ├── il.go
    │   │   ├── il_test.go
    │   │   ├── metadata.go
    │   │   ├── metadata_test.go
    │   │   ├── report.go
    │   │   └── signature.go
    │   ├── editor
    │   │   ├── amount_words.go
    │   │   ├── amount_words_test.go
    │   │   ├── base64.go
    │   │   ├── buffer_async.go
    │   │   ├── buffer_async_test.go
    │   │   ├── buffer_mapped.go
    │   │   ├── buffer_mapped_unix.go
    │   │   ├── buffer_mapped_windows.go
    │   │   ├── calculator.go
    │   │   ├── colorer_async.go
    │   │   ├── colorer_check.go
    │   │   ├── colorer_check_test.go
    │   │   ├── colorer_cpu_amd64.go
    │   │   ├── colorer_cpu_amd64_test.go
    │   │   ├── colorer_cpu.go
    │   │   ├── colorer_cpu_other.go
    │   │   ├── colorer_cpu_test.go
    │   │   ├── colorer_diagnostics_test.go
    │   │   ├── colorer_downloader.go
    │   │   ├── colorer.go
    │   │   ├── colorer_lite.go
    │   │   ├── colorer_outline_frame.go
    │   │   ├── colorer_outline_frame_test.go
    │   │   ├── colorer_outline.go
    │   │   ├── colorer_outline_test.go
    │   │   ├── colorer_pair_search.go
    │   │   ├── colorer_pairs.go
    │   │   ├── colorer_pairs_test.go
    │   │   ├── colorer_params.go
    │   │   ├── colorer_params_test.go
    │   │   ├── colorer_plugin_test.go
    │   │   ├── colorer_reload.go
    │   │   ├── colorer_setups.go
    │   │   ├── colorer_text.go
    │   │   ├── colorer_types_coverage_test.go
    │   │   ├── colorer_type_settings.go
    │   │   ├── colorer_type_settings_test.go
    │   │   ├── colorer_types.go
    │   │   ├── colorer_types_test.go
    │   │   ├── colorer_userhrd_location_test.go
    │   │   ├── colorer_userpath_test.go
    │   │   ├── colorer_window.go
    │   │   ├── editor_base64_test.go
    │   │   ├── editor_calculator_test.go
    │   │   ├── editor_codepage_test.go
    │   │   ├── editor_coverage_batch27_test.go
    │   │   ├── editor_coverage_batch28_test.go
    │   │   ├── editor_delta_test.go
    │   │   ├── editor_duplicate_line_test.go
    │   │   ├── editor_fade_test.go
    │   │   ├── editor_features_test.go
    │   │   ├── editor_find_all_test.go
    │   │   ├── editor_hex_colors_test.go
    │   │   ├── editor_highlight_budget_test.go
    │   │   ├── editor_indent_test.go
    │   │   ├── editor_index_status_test.go
    │   │   ├── editor_long_line_render_test.go
    │   │   ├── editor_mmap_test.go
    │   │   ├── editor_move_line_test.go
    │   │   ├── editor_multicursor_edit_test.go
    │   │   ├── editor_multicursor_move_test.go
    │   │   ├── editor_multicursor_occurrence_test.go
    │   │   ├── editor_multicursor_select_test.go
    │   │   ├── editor_multicursor_test.go
    │   │   ├── editor_occurrence_test.go
    │   │   ├── editor_restore_keys_test.go
    │   │   ├── editor_save_as_dialog_test.go
    │   │   ├── editor_save_as_test.go
    │   │   ├── editor_save_inplace_test.go
    │   │   ├── editor_search_lazy_test.go
    │   │   ├── editor_search_remote_test.go
    │   │   ├── editor_search_zerocopy_test.go
    │   │   ├── editor_shiftdel_test.go
    │   │   ├── editor_target_line_test.go
    │   │   ├── editor_veto_test.go
    │   │   ├── editor_view_ads_test.go
    │   │   ├── editor_view_test.go
    │   │   ├── editor_wrap_memory_test.go
    │   │   ├── editor_wrap_safety_test.go
    │   │   ├── escape_colorer_test.go
    │   │   ├── events.go
    │   │   ├── events_test.go
    │   │   ├── external_command.go
    │   │   ├── external_ctty_linux.go
    │   │   ├── external_ctty_other.go
    │   │   ├── external_editor_ctty_linux_test.go
    │   │   ├── external_editor_process_unix_test.go
    │   │   ├── external_editor_test.go
    │   │   ├── external_freebsd.go
    │   │   ├── external_freebsd_test.go
    │   │   ├── external_unix.go
    │   │   ├── external_windows.go
    │   │   ├── fade.go
    │   │   ├── far3_keys.go
    │   │   ├── far3_keys_test.go
    │   │   ├── findall.go
    │   │   ├── goto_test.go
    │   │   ├── grapheme.go
    │   │   ├── host.go
    │   │   ├── indent.go
    │   │   ├── index_status.go
    │   │   ├── issue1230_flicker_test.go
    │   │   ├── issue1230_test.go
    │   │   ├── main_test.go
    │   │   ├── mapped_file_test.go
    │   │   ├── menubar_test.go
    │   │   ├── multicursor.go
    │   │   ├── replace_confirm.go
    │   │   ├── save_as.go
    │   │   ├── search_remote.go
    │   │   ├── sort.go
    │   │   ├── sort_test.go
    │   │   ├── status_coverage_test.go
    │   │   ├── status.go
    │   │   ├── url_links_hover_test.go
    │   │   ├── view_coverage_batch23_test.go
    │   │   ├── view_coverage_contract_test.go
    │   │   ├── view.go
    │   │   ├── view_helpers_coverage_batch21_test.go
    │   │   ├── view_lowlevel_coverage_test.go
    │   │   ├── view_mdsplit.go
    │   │   ├── view_mdsplit_test.go
    │   │   ├── view_semantic_coverage_test.go
    │   │   ├── view_semantic.go
    │   │   └── wrap_safety.go
    │   ├── filemask
    │   │   ├── mask.go
    │   │   └── mask_test.go
    │   ├── fileops
    │   │   ├── access_rights.go
    │   │   ├── access_rights_test.go
    │   │   ├── archive_index_fallback.go
    │   │   ├── archive_index.go
    │   │   ├── archive_index_test.go
    │   │   ├── buttons.go
    │   │   ├── buttons_test.go
    │   │   ├── codepage.go
    │   │   ├── compare_folders_test.go
    │   │   ├── compare.go
    │   │   ├── copy_options.go
    │   │   ├── copy_options_test.go
    │   │   ├── dialog_reporter_test.go
    │   │   ├── file_mask_far2l_test.go
    │   │   ├── file_mask_test.go
    │   │   ├── file_op_dialog_test.go
    │   │   ├── file_ops_coverage_test.go
    │   │   ├── file_ops_safety_test.go
    │   │   ├── file_ops_test.go
    │   │   ├── file_ops_transfer_name_test.go
    │   │   ├── file_op_tracker_test.go
    │   │   ├── host.go
    │   │   ├── host_test.go
    │   │   ├── identity.go
    │   │   ├── issue149_test.go
    │   │   ├── issue815_test.go
    │   │   ├── local.go
    │   │   ├── main_test.go
    │   │   ├── mask.go
    │   │   ├── ops_case_test.go
    │   │   ├── ops_dialog.go
    │   │   ├── ops.go
    │   │   ├── path_identity_test.go
    │   │   ├── pump.go
    │   │   ├── queue_coverage_test.go
    │   │   ├── queue.go
    │   │   ├── queue_manager_test.go
    │   │   ├── report_contract_test.go
    │   │   ├── report.go
    │   │   ├── rights_extra.go
    │   │   ├── same_device_move_test.go
    │   │   ├── security_other.go
    │   │   ├── security_windows.go
    │   │   ├── security_windows_test.go
    │   │   ├── state.go
    │   │   ├── state_key_test.go
    │   │   ├── state_test.go
    │   │   ├── symlink_coverage_test.go
    │   │   ├── symlink.go
    │   │   ├── sync.go
    │   │   ├── sync_test.go
    │   │   └── tracker.go
    │   ├── frameborder
    │   │   ├── frameborder.go
    │   │   └── frameborder_test.go
    │   ├── fusefs
    │   │   ├── bench-all.sh
    │   │   ├── BENCH.md
    │   │   ├── bench.sh
    │   │   ├── bridge.go
    │   │   ├── bridge_test.go
    │   │   ├── cli_coverage_signal_test.go
    │   │   ├── cli_coverage_test.go
    │   │   ├── cli.go
    │   │   ├── cli_test.go
    │   │   ├── coverage_helpers_test.go
    │   │   ├── fusefs.go
    │   │   ├── FUSE.md
    │   │   ├── mountspec.go
    │   │   ├── node_fuse.go
    │   │   ├── node_fuse_test.go
    │   │   ├── node_unsupported.go
    │   │   ├── platform_other.go
    │   │   ├── platform_unix.go
    │   │   ├── registry.go
    │   │   ├── staged_test.go
    │   │   └── writers_test.go
    │   ├── gui
    │   │   ├── assets
    │   │   │   └── icon
    │   │   │       ├── embed.go
    │   │   │       ├── f4-16.svg
    │   │   │       ├── f4-24.svg
    │   │   │       ├── f4-30.svg
    │   │   │       ├── f4-32.svg
    │   │   │       ├── f4-36.svg
    │   │   │       ├── f4-42.svg
    │   │   │       ├── f4.svg
    │   │   │       ├── generated
    │   │   │       │   ├── f4-1024.png
    │   │   │       │   ├── f4-128.png
    │   │   │       │   ├── f4-16.png
    │   │   │       │   ├── f4-24.png
    │   │   │       │   ├── f4-256.png
    │   │   │       │   ├── f4-28.png
    │   │   │       │   ├── f4-30.png
    │   │   │       │   ├── f4-32.png
    │   │   │       │   ├── f4-36.png
    │   │   │       │   ├── f4-42.png
    │   │   │       │   ├── f4-48.png
    │   │   │       │   ├── f4-512.png
    │   │   │       │   ├── f4-56.png
    │   │   │       │   ├── f4-64.png
    │   │   │       │   ├── f4.icns
    │   │   │       │   └── f4.ico
    │   │   │       └── README.md
    │   │   ├── backend_ffi.go
    │   │   ├── backend.go
    │   │   ├── backends.go
    │   │   ├── backends_lite.go
    │   │   ├── backends_lite_test.go
    │   │   ├── backends_test.go
    │   │   ├── backends_tty.go
    │   │   ├── backends_tty_test.go
    │   │   ├── backend_stub.go
    │   │   ├── backend_test.go
    │   │   ├── font_catalog.go
    │   │   ├── font_catalog_test.go
    │   │   ├── font_catalog_unix.go
    │   │   ├── font_catalog_windows.go
    │   │   ├── font_combo.go
    │   │   ├── font.go
    │   │   ├── font_notwindows.go
    │   │   ├── font_test.go
    │   │   ├── font_windows.go
    │   │   ├── font_windows_test.go
    │   │   ├── icon_darwin.go
    │   │   ├── icon_darwin_test.go
    │   │   ├── icon_stub.go
    │   │   ├── icon_unix.go
    │   │   ├── icon_windows_coverage_test.go
    │   │   ├── icon_windows.go
    │   │   ├── icon_windows_test.go
    │   │   ├── liteguard.go
    │   │   ├── runtime_mode.go
    │   │   ├── runtime_mode_test.go
    │   │   ├── run_unix.go
    │   │   ├── run_unix_test.go
    │   │   ├── run_windows.go
    │   │   ├── window.go
    │   │   ├── winepath_other.go
    │   │   ├── winepath_windows.go
    │   │   └── winepath_windows_test.go
    │   ├── hideconsole
    │   │   ├── go.mod
    │   │   └── hideconsole.go
    │   ├── history
    │   │   ├── edit.go
    │   │   ├── edit_test.go
    │   │   ├── far2l.go
    │   │   ├── history_coverage_test.go
    │   │   ├── menu.go
    │   │   ├── paths.go
    │   │   ├── pins.go
    │   │   ├── provider.go
    │   │   ├── shell.go
    │   │   └── shell_test.go
    │   ├── hostwidth
    │   │   ├── hostwidth.go
    │   │   └── hostwidth_test.go
    │   ├── i18n
    │   │   ├── lang
    │   │   │   ├── ar.lng
    │   │   │   ├── be.lng
    │   │   │   ├── bn.lng
    │   │   │   ├── coverage_baseline.txt
    │   │   │   ├── cs.lng
    │   │   │   ├── de.lng
    │   │   │   ├── en.lng
    │   │   │   ├── es.lng
    │   │   │   ├── et.lng
    │   │   │   ├── fi.lng
    │   │   │   ├── he.lng
    │   │   │   ├── hi.lng
    │   │   │   ├── hu.lng
    │   │   │   ├── hy.lng
    │   │   │   ├── ja.lng
    │   │   │   ├── ka.lng
    │   │   │   ├── ko.lng
    │   │   │   ├── lt.lng
    │   │   │   ├── lv.lng
    │   │   │   ├── pl.lng
    │   │   │   ├── README.md
    │   │   │   ├── ru.lng
    │   │   │   ├── tr.lng
    │   │   │   ├── uk.lng
    │   │   │   └── zh.lng
    │   │   ├── lang_bidi_test.go
    │   │   ├── lang_consistency_test.go
    │   │   ├── lang_contamination_test.go
    │   │   ├── lang_fallback_priority_test.go
    │   │   ├── langfs_extralite.go
    │   │   ├── langfs.go
    │   │   ├── lang.go
    │   │   ├── lang_homoglyphs_test.go
    │   │   ├── lang_packs_test.go
    │   │   ├── lang_ru_complete_test.go
    │   │   ├── lang_scripts_test.go
    │   │   ├── lang_test.go
    │   │   ├── language_list_test.go
    │   │   ├── languages.go
    │   │   ├── msg_keys_test.go
    │   │   ├── packs.go
    │   │   └── settings_translations_test.go
    │   ├── ini
    │   │   ├── ini.go
    │   │   └── ini_test.go
    │   ├── install
    │   │   ├── cli.go
    │   │   ├── cli_test.go
    │   │   ├── desktop.go
    │   │   ├── desktop_test.go
    │   │   ├── install.go
    │   │   └── install_test.go
    │   ├── keymap
    │   │   ├── farkeys.go
    │   │   ├── farkeys_selection_test.go
    │   │   ├── hotkeys.go
    │   │   ├── input.go
    │   │   ├── input_translation_test.go
    │   │   ├── keybar_combined_rows_test.go
    │   │   ├── keybar_labels_enabled_test.go
    │   │   ├── keymap_test.go
    │   │   ├── kitty_coverage_batch39_test.go
    │   │   ├── kitty_coverage_extra_test.go
    │   │   ├── kitty.go
    │   │   ├── mackeys.go
    │   │   ├── mackeys_test.go
    │   │   ├── remap.go
    │   │   ├── terminal_mouse_offset_test.go
    │   │   ├── translate_kitty_test.go
    │   │   ├── ttyx.go
    │   │   └── ttyx_keys_test.go
    │   ├── luaplug
    │   │   ├── convert.go
    │   │   ├── convert_test.go
    │   │   ├── f4rpc.go
    │   │   ├── ffi.go
    │   │   ├── ffi_test.go
    │   │   ├── goid.go
    │   │   ├── luastate_test.go
    │   │   ├── runtime_deadline_test.go
    │   │   ├── runtime.go
    │   │   ├── runtime_test.go
    │   │   └── sandbox.go
    │   ├── macro
    │   │   ├── engine.go
    │   │   ├── engine_test.go
    │   │   ├── export.go
    │   │   ├── export_test.go
    │   │   ├── lua_api_coverage_batch19_test.go
    │   │   ├── lua_api.go
    │   │   ├── lua_compat.go
    │   │   ├── lua_events_test.go
    │   │   ├── lua_extralite.go
    │   │   ├── lua.go
    │   │   ├── lua_mf_extra.go
    │   │   ├── lua_mf_extra_test.go
    │   │   ├── lua_test.go
    │   │   ├── lua_timer.go
    │   │   ├── lua_types.go
    │   │   ├── plugin_calls.go
    │   │   ├── plugin_calls_test.go
    │   │   ├── reload.go
    │   │   └── reload_test.go
    │   ├── mdmath
    │   │   ├── convert.go
    │   │   ├── mdmath_test.go
    │   │   └── prepare.go
    │   ├── media
    │   │   ├── ansi_art.go
    │   │   ├── ansi_art_test.go
    │   │   ├── application.go
    │   │   ├── audio_context_reuse_test.go
    │   │   ├── audio_decode_bad_stream_coverage_test.go
    │   │   ├── audio_decode_contract_test.go
    │   │   ├── audio_decode_coverage_extra_test.go
    │   │   ├── audio_decode_coverage_test.go
    │   │   ├── audio_decode_external_coverage_test.go
    │   │   ├── audio_decode_flac_coverage_test.go
    │   │   ├── audio_decode.go
    │   │   ├── audio_decode_test.go
    │   │   ├── audio_engine_contract_test.go
    │   │   ├── audio_format.go
    │   │   ├── audio_format_test.go
    │   │   ├── audio.go
    │   │   ├── audio_oto.go
    │   │   ├── audio_stub.go
    │   │   ├── blit_test.go
    │   │   ├── image_bmp_coverage_test.go
    │   │   ├── image_bmp.go
    │   │   ├── image_console_stats.go
    │   │   ├── image_console_stats_test.go
    │   │   ├── image_decode.go
    │   │   ├── image_decode_test.go
    │   │   ├── image_encode.go
    │   │   ├── image_encode_test.go
    │   │   ├── image_external.go
    │   │   ├── image_external_test.go
    │   │   ├── image_formats_test.go
    │   │   ├── image_gallery_coverage_test.go
    │   │   ├── image_gallery.go
    │   │   ├── image_gallery_test.go
    │   │   ├── image.go
    │   │   ├── image_native_darwin.go
    │   │   ├── image_native_darwin_test.go
    │   │   ├── image_preview_coverage_test.go
    │   │   ├── image_preview.go
    │   │   ├── image_preview_parsing_coverage_test.go
    │   │   ├── image_preview_success_coverage_test.go
    │   │   ├── image_preview_test.go
    │   │   ├── image_qoi_coverage_test.go
    │   │   ├── image_qoi.go
    │   │   ├── image_quick_preview_coverage_test.go
    │   │   ├── image_slideshow.go
    │   │   ├── image_slideshow_test.go
    │   │   ├── image_test.go
    │   │   ├── image_transform.go
    │   │   ├── image_transform_test.go
    │   │   ├── image_view_coverage_extra_test.go
    │   │   ├── image_view.go
    │   │   ├── image_view_halfblocks_test.go
    │   │   ├── image_view_orient_test.go
    │   │   ├── image_view_overlay_test.go
    │   │   ├── image_view_test.go
    │   │   ├── overlay_console.go
    │   │   ├── overlay_console_key_test.go
    │   │   ├── overlay_x11.go
    │   │   ├── overlay_x11_test.go
    │   │   ├── tools.go
    │   │   ├── tools_test.go
    │   │   ├── video_frames.go
    │   │   ├── video_frames_test.go
    │   │   ├── video_frame_view.go
    │   │   ├── video_frame_view_test.go
    │   │   ├── video.go
    │   │   ├── video_state.go
    │   │   ├── video_state_test.go
    │   │   ├── video_test.go
    │   │   ├── video_view_coverage_test.go
    │   │   └── video_view.go
    │   ├── menuhotkeys
    │   │   ├── menuhotkeys.go
    │   │   └── menuhotkeys_test.go
    │   ├── mermaid
    │   │   ├── class.go
    │   │   ├── mermaid.go
    │   │   ├── mermaid_test.go
    │   │   ├── more.go
    │   │   └── sequence.go
    │   ├── netproxy
    │   │   ├── coverage_test.go
    │   │   ├── keepalive_linux_test.go
    │   │   ├── netproxy_gap_test.go
    │   │   ├── netproxy.go
    │   │   └── netproxy_test.go
    │   ├── numeric
    │   │   ├── memory.go
    │   │   ├── numeric.go
    │   │   ├── numeric_test.go
    │   │   └── size.go
    │   ├── numwords
    │   │   ├── numwords.go
    │   │   └── numwords_test.go
    │   ├── panel
    │   │   ├── actions_coverage_test.go
    │   │   ├── actions.go
    │   │   ├── apply_coverage_batch29_test.go
    │   │   ├── apply_coverage_extra_test.go
    │   │   ├── apply.go
    │   │   ├── apply_shutdown.go
    │   │   ├── associations_editor.go
    │   │   ├── associations.go
    │   │   ├── associations_ui.go
    │   │   ├── audio_decode_panel_test.go
    │   │   ├── autofilter.go
    │   │   ├── background_jobs_session_test.go
    │   │   ├── base64_file_coverage_test.go
    │   │   ├── base64_file.go
    │   │   ├── base64_file_test.go
    │   │   ├── bookmarks_dialog.go
    │   │   ├── bookmarks_dialog_test.go
    │   │   ├── bookmarks.go
    │   │   ├── bookmarks_plugin.go
    │   │   ├── bridge_texteditor.go
    │   │   ├── bridge_visren_coverage_test.go
    │   │   ├── bridge_visren.go
    │   │   ├── clipboard_image.go
    │   │   ├── clipboard_image_test.go
    │   │   ├── cmd_session_test.go
    │   │   ├── column_resize_test.go
    │   │   ├── console.go
    │   │   ├── console_rows_test.go
    │   │   ├── context_editors_test.go
    │   │   ├── coverage_player_helpers_test.go
    │   │   ├── custom_column_modes_test.go
    │   │   ├── dirwatch.go
    │   │   ├── dirwatch_test.go
    │   │   ├── drive_menu_headings_test.go
    │   │   ├── drives_bookmarks.go
    │   │   ├── drives_bookmarks_test.go
    │   │   ├── drives_bookmarks_ui.go
    │   │   ├── drives_bookmarks_ui_test.go
    │   │   ├── drives_menu.go
    │   │   ├── drives_menu_ampersand_test.go
    │   │   ├── drives_menu_unix.go
    │   │   ├── drives_menu_windows.go
    │   │   ├── drives_tools_order.go
    │   │   ├── drives_tools_order_test.go
    │   │   ├── edit_command_test.go
    │   │   ├── entry_totals_cache_test.go
    │   │   ├── exec.go
    │   │   ├── file_clipboard.go
    │   │   ├── file_clipboard_test.go
    │   │   ├── file_panel_test.go
    │   │   ├── folder_event_test.go
    │   │   ├── frame_coverage_batch22_test.go
    │   │   ├── frame_coverage_batch25_test.go
    │   │   ├── frame_coverage_test.go
    │   │   ├── frame_dragdrop.go
    │   │   ├── frame_dragdrop_nested.go
    │   │   ├── frame_dragdrop_term.go
    │   │   ├── frame_dragdrop_vfs.go
    │   │   ├── frame_externalui.go
    │   │   ├── frame.go
    │   │   ├── frame_helpers_coverage_test.go
    │   │   ├── frame_history_coverage_test.go
    │   │   ├── frame_manager_test_helpers_test.go
    │   │   ├── frame_procenv.go
    │   │   ├── frame_semantic.go
    │   │   ├── frame_shell_darwin_test.go
    │   │   ├── frame_translator.go
    │   │   ├── frame_workspace.go
    │   │   ├── frame_workspace_terminal.go
    │   │   ├── fuse_list_coverage_extra_test.go
    │   │   ├── fuse_list_coverage_test.go
    │   │   ├── fuse_list.go
    │   │   ├── fuse_mount_coverage_test.go
    │   │   ├── fuse_mount.go
    │   │   ├── grouping.go
    │   │   ├── grouping_menu_coverage_test.go
    │   │   ├── grouping_menu.go
    │   │   ├── grouping_test.go
    │   │   ├── grouping_view.go
    │   │   ├── header_label_zone_test.go
    │   │   ├── hints.go
    │   │   ├── host_console_replies.go
    │   │   ├── host_console_replies_test.go
    │   │   ├── host_coverage_test.go
    │   │   ├── host.go
    │   │   ├── host_input_modes.go
    │   │   ├── host_input_modes_other.go
    │   │   ├── host_input_modes_test.go
    │   │   ├── host_input_modes_windows.go
    │   │   ├── hotkey_conditions.go
    │   │   ├── info.go
    │   │   ├── info_panel_test.go
    │   │   ├── info_usage.go
    │   │   ├── insert_dir_path_test.go
    │   │   ├── issue1131_focus_test.go
    │   │   ├── issue1131_test.go
    │   │   ├── issue1184_test.go
    │   │   ├── issue1218_keybar_test.go
    │   │   ├── issue1234_test.go
    │   │   ├── issue1320_keybar_test.go
    │   │   ├── issue863_terminal_test.go
    │   │   ├── issue915_keybar_test.go
    │   │   ├── kitty_passthrough.go
    │   │   ├── kitty_passthrough_test.go
    │   │   ├── layout_selection_coverage_test.go
    │   │   ├── lazy_progress_test.go
    │   │   ├── list_access.go
    │   │   ├── list_access_other_test.go
    │   │   ├── list_access_test.go
    │   │   ├── list_access_windows_test.go
    │   │   ├── list.go
    │   │   ├── list_reconnect.go
    │   │   ├── list_size.go
    │   │   ├── list_size_test.go
    │   │   ├── lone_alt.go
    │   │   ├── lone_alt_test.go
    │   │   ├── lookup.go
    │   │   ├── main_test.go
    │   │   ├── managed_exec_debounce_test.go
    │   │   ├── menubar_dropdown_test.go
    │   │   ├── menu_cache_test.go
    │   │   ├── menukeys.go
    │   │   ├── menukeys_test.go
    │   │   ├── mock_pty_test.go
    │   │   ├── namecompare_extralite.go
    │   │   ├── namecompare.go
    │   │   ├── navigate_elevated_async_test.go
    │   │   ├── navigation_guards_coverage_test.go
    │   │   ├── navigation_vfs_coverage_test.go
    │   │   ├── panels_frame_pty_test.go
    │   │   ├── panels_frame_test.go
    │   │   ├── pins_coverage_test.go
    │   │   ├── pins.go
    │   │   ├── player_coverage_extra_test.go
    │   │   ├── player_coverage_test.go
    │   │   ├── player.go
    │   │   ├── player_test.go
    │   │   ├── plugin_default_hotkeys_test.go
    │   │   ├── plugin_hotkey_dialog.go
    │   │   ├── plugin_hotkey_dialog_test.go
    │   │   ├── plugin_hotkeys.go
    │   │   ├── plugin_menu_commands_test.go
    │   │   ├── plugins_closekey_test.go
    │   │   ├── plugins.go
    │   │   ├── plugins_swap_test.go
    │   │   ├── prefixes.go
    │   │   ├── prefixes_registry_windows_test.go
    │   │   ├── prefixes_test.go
    │   │   ├── press_key_test.go
    │   │   ├── proactive_kitty_protocol_test.go
    │   │   ├── process_environment_panel_test.go
    │   │   ├── progress_overlay_test.go
    │   │   ├── prompt_coverage_extra_test.go
    │   │   ├── prompt.go
    │   │   ├── pty_reader.go
    │   │   ├── pty_reader_test.go
    │   │   ├── quickview_colors_test.go
    │   │   ├── quickview_coverage_test.go
    │   │   ├── quickview.go
    │   │   ├── quick_view_panel_test.go
    │   │   ├── quick_view_provider_test.go
    │   │   ├── quick_view_refused_test.go
    │   │   ├── reconnect_test.go
    │   │   ├── remote.go
    │   │   ├── selection_extension.go
    │   │   ├── selection_extension_test.go
    │   │   ├── selection_panel_test.go
    │   │   ├── session_cmd.go
    │   │   ├── settings.go
    │   │   ├── settings_test.go
    │   │   ├── shell_session_test.go
    │   │   ├── shell_start_dir_test.go
    │   │   ├── smb_target_test.go
    │   │   ├── sort.go
    │   │   ├── sort_groups_test.go
    │   │   ├── sort_natural.go
    │   │   ├── sort_natural_test.go
    │   │   ├── state.go
    │   │   ├── strict_autofilter_test.go
    │   │   ├── symlink_target.go
    │   │   ├── symlink_target_test.go
    │   │   ├── temp_coverage_test.go
    │   │   ├── temp_dragout_test.go
    │   │   ├── temp.go
    │   │   ├── terminal_redraw_target_test.go
    │   │   ├── tree.go
    │   │   ├── tree_persist.go
    │   │   ├── tree_persist_test.go
    │   │   ├── tree_test.go
    │   │   ├── uri_navigation_test.go
    │   │   ├── usermenu_coverage_test.go
    │   │   ├── usermenu_farfile.go
    │   │   ├── usermenu.go
    │   │   ├── usermenu_ini.go
    │   │   ├── usermenu_script_coverage_test.go
    │   │   ├── usermenu_script.go
    │   │   ├── usermenu_subst.go
    │   │   ├── usermenu_subst_test.go
    │   │   ├── usermenu_ui_coverage_extra_test.go
    │   │   ├── usermenu_ui.go
    │   │   ├── user_menu_ui_test.go
    │   │   ├── viewmodes_columns.go
    │   │   ├── viewmodes_dialog_coverage_test.go
    │   │   ├── viewmodes_dialog.go
    │   │   ├── viewmodes.go
    │   │   ├── viewmodes_test.go
    │   │   ├── wheel.go
    │   │   ├── wheel_test.go
    │   │   ├── workspace_coverage_batch38_test.go
    │   │   ├── workspace.go
    │   │   └── workspace_startup_test.go
    │   ├── paneltest
    │   │   ├── doc.go
    │   │   ├── frame.go
    │   │   └── mock_pty.go
    │   ├── pdftext
    │   │   ├── doc.go
    │   │   ├── extract.go
    │   │   ├── images.go
    │   │   ├── parse.go
    │   │   ├── pdftext_test.go
    │   │   └── text.go
    │   ├── piecetable
    │   │   ├── concurrent_test.go
    │   │   ├── lineindex_edgecases_test.go
    │   │   ├── lineindex_equivalence_test.go
    │   │   ├── lineindex.go
    │   │   ├── lineindex_test.go
    │   │   ├── piecetable.go
    │   │   └── piecetable_test.go
    │   ├── plughost
    │   │   ├── application.go
    │   │   ├── contributions.go
    │   │   ├── extui_coverage_test.go
    │   │   ├── extui.go
    │   │   ├── extui_test.go
    │   │   ├── ffi.go
    │   │   ├── ffi_test.go
    │   │   ├── host_coverage_test.go
    │   │   ├── host.go
    │   │   ├── identity_test.go
    │   │   ├── lifecycle_coverage_test.go
    │   │   ├── manager_coverage_test.go
    │   │   ├── manager.go
    │   │   ├── manager_lifecycle_test.go
    │   │   ├── manager_names_test.go
    │   │   ├── menu_items.go
    │   │   ├── panel_providers.go
    │   │   ├── permissions.go
    │   │   ├── permissions_test.go
    │   │   ├── permissions_ui.go
    │   │   ├── permissions_ui_test.go
    │   │   ├── plugin_entrypoints.go
    │   │   ├── plugins_full.go
    │   │   ├── plugins_internal_extralite.go
    │   │   ├── plugins_internal.go
    │   │   ├── plugins_lite.go
    │   │   ├── plugins_observer_extralite.go
    │   │   ├── plugins_observer.go
    │   │   ├── plugring_firstparty.go
    │   │   ├── plugring_firstparty_test.go
    │   │   ├── plugring.go
    │   │   ├── plugring_meta.go
    │   │   ├── plugring_meta_test.go
    │   │   ├── rpc_commands.go
    │   │   ├── rpc_panel_coverage_test.go
    │   │   ├── rpc_panel.go
    │   │   ├── rpc_panel_keys_test.go
    │   │   ├── rpc_vfs_coverage_test.go
    │   │   ├── rpc_vfs.go
    │   │   ├── rpc_vfs_test.go
    │   │   ├── scaffold.go
    │   │   ├── scaffold_test.go
    │   │   ├── settings_permissions_test.go
    │   │   ├── transport_lua_extralite.go
    │   │   ├── transport_lua.go
    │   │   ├── transport_lua_rpc_test.go
    │   │   ├── transport_lua_test.go
    │   │   ├── transport_rpc_coverage_test.go
    │   │   ├── transport_rpc.go
    │   │   ├── transport_rpc_test.go
    │   │   ├── transport_wazero_extralite.go
    │   │   ├── transport_wazero.go
    │   │   ├── transport_wazero_test.go
    │   │   ├── ui_guard.go
    │   │   └── ui_guard_test.go
    │   ├── semantic
    │   │   ├── fields.go
    │   │   └── fields_test.go
    │   ├── settings
    │   │   ├── benchmark_test.go
    │   │   ├── catalog.go
    │   │   ├── center.go
    │   │   ├── center_test.go
    │   │   ├── choice_help.go
    │   │   ├── choice_help_test.go
    │   │   ├── choice_help.tsv
    │   │   ├── chord_coverage_test.go
    │   │   ├── chord.go
    │   │   ├── clipboard_image_test.go
    │   │   ├── collection_palette_test.go
    │   │   ├── collection_ui.go
    │   │   ├── config_docs.go
    │   │   ├── config_docs_test.go
    │   │   ├── core.go
    │   │   ├── edit.go
    │   │   ├── edit_test.go
    │   │   ├── enter_test.go
    │   │   ├── extra.go
    │   │   ├── grouping_test.go
    │   │   ├── help.go
    │   │   ├── host.go
    │   │   ├── hotkeys_persistence_test.go
    │   │   ├── inventory_test.go
    │   │   ├── issue320_test.go
    │   │   ├── main_test.go
    │   │   ├── manual_save_test.go
    │   │   ├── mouse_test.go
    │   │   ├── operations_catalog_coverage_batch51_test.go
    │   │   ├── operations_coverage_test.go
    │   │   ├── operations.go
    │   │   ├── plugin_catalog.go
    │   │   ├── plugin_providers_coverage_test.go
    │   │   ├── plugins.go
    │   │   ├── providers.go
    │   │   ├── radios.go
    │   │   ├── radios_test.go
    │   │   ├── records_coverage_test.go
    │   │   ├── records.go
    │   │   ├── russian_test.go
    │   │   ├── scoped_test.go
    │   │   ├── search_layout_test.go
    │   │   ├── startup_test.go
    │   │   ├── trace.go
    │   │   ├── trace_test.go
    │   │   ├── transactions_test.go
    │   │   ├── user_menu.go
    │   │   └── user_menu_test.go
    │   ├── settingstest
    │   │   └── russian.go
    │   ├── sheet
    │   │   ├── cell.go
    │   │   ├── coverage_contract_test.go
    │   │   ├── expr.go
    │   │   ├── sheet.go
    │   │   ├── sheet_test.go
    │   │   ├── store.go
    │   │   ├── store_lite.go
    │   │   ├── store_lite_test.go
    │   │   └── xlsx.go
    │   ├── stallwatch
    │   │   ├── stallwatch.go
    │   │   └── stallwatch_test.go
    │   ├── sysinfo
    │   │   ├── cpu_darwin.go
    │   │   ├── cpu.go
    │   │   ├── cpu_linux.go
    │   │   ├── cpu_linux_test.go
    │   │   ├── cpu_other.go
    │   │   ├── cpu_windows.go
    │   │   ├── drives.go
    │   │   ├── drives_test.go
    │   │   ├── drives_unix.go
    │   │   ├── drives_unix_test.go
    │   │   ├── drives_windows.go
    │   │   ├── fs_darwin.go
    │   │   ├── fs.go
    │   │   ├── fs_linux.go
    │   │   ├── fs_linux_test.go
    │   │   ├── fs_other.go
    │   │   ├── fs_windows.go
    │   │   ├── fs_windows_test.go
    │   │   ├── gpu_darwin.go
    │   │   ├── gpu.go
    │   │   ├── gpu_linux.go
    │   │   ├── gpu_linux_test.go
    │   │   ├── gpu_other.go
    │   │   ├── gpu_windows.go
    │   │   ├── mem.go
    │   │   ├── mem_linux.go
    │   │   ├── mem_linux_test.go
    │   │   ├── mem_other.go
    │   │   ├── mem_windows.go
    │   │   ├── mounts_linux.go
    │   │   ├── mounts_linux_test.go
    │   │   └── mounts_other.go
    │   ├── tarindexcache
    │   │   ├── tarindexcache.go
    │   │   └── tarindexcache_test.go
    │   ├── terminal
    │   │   ├── ansi_coverage_extra_test.go
    │   │   ├── ansi.go
    │   │   ├── ansi_sync_test.go
    │   │   ├── ansi_test.go
    │   │   ├── application.go
    │   │   ├── attr.go
    │   │   ├── backend.go
    │   │   ├── child_env.go
    │   │   ├── child_process.go
    │   │   ├── clipboard_async.go
    │   │   ├── clipboard.go
    │   │   ├── clipboard_image.go
    │   │   ├── clipboard_image_test.go
    │   │   ├── clipboard_test.go
    │   │   ├── conpty_package.go
    │   │   ├── conpty_package_test.go
    │   │   ├── console_buffer_other.go
    │   │   ├── console_buffer_windows.go
    │   │   ├── console_cmdline.go
    │   │   ├── console_cmdline_test.go
    │   │   ├── console_host_windows.go
    │   │   ├── console_overlay_other.go
    │   │   ├── console_overlay_windows.go
    │   │   ├── console_overlay_windows_test.go
    │   │   ├── console_palette_windows.go
    │   │   ├── console_palette_windows_test.go
    │   │   ├── console_scroll.go
    │   │   ├── console_scroll_other.go
    │   │   ├── console_scroll_test.go
    │   │   ├── console_scroll_windows.go
    │   │   ├── console_spawn_other.go
    │   │   ├── console_spawn_windows.go
    │   │   ├── far2l_auth.go
    │   │   ├── far2l_auth_test.go
    │   │   ├── far2ldnd
    │   │   │   ├── far2ldnd_test.go
    │   │   │   ├── frame.go
    │   │   │   ├── messages.go
    │   │   │   └── stack.go
    │   │   ├── far2l_dnd_bridge.go
    │   │   ├── far2l_dnd_client.go
    │   │   ├── far2l_dnd_client_test.go
    │   │   ├── far2l_dnd.go
    │   │   ├── far2l_dnd_proxy.go
    │   │   ├── far2l_dnd_test.go
    │   │   ├── far2l_image.go
    │   │   ├── far2l_image_test.go
    │   │   ├── graphics_compat.go
    │   │   ├── graphics_compat_test.go
    │   │   ├── graphics_probe.go
    │   │   ├── graphics_probe_test.go
    │   │   ├── graphics_probe_windows.go
    │   │   ├── iterm2.go
    │   │   ├── iterm2_test.go
    │   │   ├── jobs.go
    │   │   ├── jobs_test.go
    │   │   ├── kitty_coverage_test.go
    │   │   ├── kitty_diacritics.go
    │   │   ├── kitty.go
    │   │   ├── kitty_metrics_test.go
    │   │   ├── kitty_placeholder.go
    │   │   ├── kitty_placeholder_test.go
    │   │   ├── kitty_placements.go
    │   │   ├── kitty_placements_test.go
    │   │   ├── kitty_test.go
    │   │   ├── local_command_capture.go
    │   │   ├── local_command_capture_test.go
    │   │   ├── local_command_inline_other.go
    │   │   ├── local_command_inline_windows.go
    │   │   ├── log_console_other.go
    │   │   ├── log_console_windows.go
    │   │   ├── log_vfs_coverage_test.go
    │   │   ├── log_vfs.go
    │   │   ├── log_vfs_test.go
    │   │   ├── main_test.go
    │   │   ├── managed_exec.go
    │   │   ├── managed_exec_test.go
    │   │   ├── native_command_other.go
    │   │   ├── native_command_windows.go
    │   │   ├── overlay.go
    │   │   ├── pe_subsystem.go
    │   │   ├── pe_subsystem_test.go
    │   │   ├── process_environment.go
    │   │   ├── process_environment_runtime_unix.go
    │   │   ├── process_environment_runtime_windows.go
    │   │   ├── process_environment_test.go
    │   │   ├── pty_bsd_dragonfly.go
    │   │   ├── pty_bsd_freebsd.go
    │   │   ├── pty_bsd.go
    │   │   ├── pty_bsd_test.go
    │   │   ├── pty_cloexec_test.go
    │   │   ├── pty_darwin.go
    │   │   ├── pty_diag_unix.go
    │   │   ├── pty_diag_unix_test.go
    │   │   ├── pty_diag_windows.go
    │   │   ├── pty.go
    │   │   ├── pty_linux.go
    │   │   ├── pty_logical_lines.go
    │   │   ├── pty_logical_lines_solaris.go
    │   │   ├── pty_logical_lines_unix.go
    │   │   ├── pty_pollable_test.go
    │   │   ├── pty_ptm.go
    │   │   ├── pty_ptm_netbsd.go
    │   │   ├── pty_ptm_openbsd.go
    │   │   ├── pty_solaris.go
    │   │   ├── pty_test.go
    │   │   ├── pty_windows.go
    │   │   ├── pty_windows_test.go
    │   │   ├── pty_wine_other.go
    │   │   ├── pty_wine_shell.go
    │   │   ├── pty_wine_shell_test.go
    │   │   ├── pty_wine_windows.go
    │   │   ├── redraw.go
    │   │   ├── redraw_stop_test.go
    │   │   ├── runner.go
    │   │   ├── runner_test.go
    │   │   ├── runner_unix.go
    │   │   ├── runner_unix_test.go
    │   │   ├── runner_windows.go
    │   │   ├── runner_windows_test.go
    │   │   ├── selection_test.go
    │   │   ├── server_diagnostics.go
    │   │   ├── session_attach_identity_test.go
    │   │   ├── session_attach_payload_test.go
    │   │   ├── session_daemon_test.go
    │   │   ├── session_unix_coverage_extra_test.go
    │   │   ├── session_unix_coverage_test.go
    │   │   ├── session_unix.go
    │   │   ├── session_unix_test.go
    │   │   ├── session_windows.go
    │   │   ├── shellmode.go
    │   │   ├── shellmode_test.go
    │   │   ├── shell_start_darwin.go
    │   │   ├── shell_start_other.go
    │   │   ├── sixel.go
    │   │   ├── sixel_layers_test.go
    │   │   ├── sixel_terminal.go
    │   │   ├── sixel_terminal_test.go
    │   │   ├── sixel_test.go
    │   │   ├── solaris_pty_alloc_test.go
    │   │   ├── solaris_pty_backend_test.go
    │   │   ├── solaris_pty.go
    │   │   ├── solaris_streams.go
    │   │   ├── solaris_streams_mock_linux_test.go
    │   │   ├── solaris_streams_mock_other_test.go
    │   │   ├── solaris_streams_mock_test.go
    │   │   ├── solaris_streams_test.go
    │   │   ├── ttyx_probe_coverage_unix_test.go
    │   │   ├── ttyx_probe.go
    │   │   ├── ttyx_probe_parse.go
    │   │   ├── ttyx_probe_test.go
    │   │   ├── ttyx_probe_unix.go
    │   │   ├── ttyx_probe_windows.go
    │   │   ├── ttyx_session.go
    │   │   ├── view_coverage_extra_test.go
    │   │   ├── view_defaultcolors.go
    │   │   ├── view_defaultcolors_test.go
    │   │   ├── view.go
    │   │   ├── view_reflow.go
    │   │   ├── view_reflow_test.go
    │   │   ├── view_semantic.go
    │   │   ├── view_semantic_test.go
    │   │   ├── view_test.go
    │   │   ├── wineprobe_escape_other.go
    │   │   ├── wineprobe_escape_windows.go
    │   │   ├── wineprobe.go
    │   │   ├── wineprobe_other.go
    │   │   ├── wineprobe_test.go
    │   │   ├── wineprobe_windows.go
    │   │   └── zzz_pty_leak_check_test.go
    │   ├── testutil
    │   │   ├── dialog.go
    │   │   ├── doc.go
    │   │   ├── frame.go
    │   │   ├── frame_test.go
    │   │   ├── input.go
    │   │   ├── main.go
    │   │   ├── numeric.go
    │   │   ├── paths_coverage_test.go
    │   │   ├── paths.go
    │   │   ├── race_disabled.go
    │   │   ├── race_enabled.go
    │   │   └── rpc.go
    │   ├── textdiff
    │   │   ├── textdiff.go
    │   │   └── textdiff_test.go
    │   ├── textlayout
    │   │   ├── cluster.go
    │   │   ├── wrap.go
    │   │   └── wrap_test.go
    │   ├── textsearch
    │   │   ├── search.go
    │   │   └── search_test.go
    │   ├── theme
    │   │   ├── colors_background_inheritance_test.go
    │   │   ├── colors_background_test.go
    │   │   ├── colors_cursor_test.go
    │   │   ├── colors.go
    │   │   ├── colorspace.go
    │   │   ├── colorspace_test.go
    │   │   ├── colors_test.go
    │   │   ├── color_validate.go
    │   │   ├── color_validate_test.go
    │   │   ├── farcolor.go
    │   │   ├── farcolor_test.go
    │   │   ├── highlight_cache_test.go
    │   │   ├── highlight_edgecases_test.go
    │   │   ├── highlight_files_test.go
    │   │   ├── highlight.go
    │   │   ├── style_combo_colors_test.go
    │   │   ├── style_completeness_test.go
    │   │   ├── style_custom_test.go
    │   │   ├── style_default_dark_test.go
    │   │   ├── style.go
    │   │   ├── style_indicator_test.go
    │   │   ├── style_missing_section_test.go
    │   │   ├── style_overrides_test.go
    │   │   ├── styles
    │   │   │   ├── classic.ini
    │   │   │   ├── default_dark.ini
    │   │   │   ├── mc_dark.ini
    │   │   │   ├── mc_default.ini
    │   │   │   ├── modern.ini
    │   │   │   ├── radiola.ini
    │   │   │   └── radiola.md
    │   │   ├── style_test.go
    │   │   └── table.go
    │   ├── toast
    │   │   └── toast.go
    │   ├── ttyx
    │   │   ├── coverage_edges_test.go
    │   │   ├── coverage_no_display_test.go
    │   │   ├── coverage_reject_test.go
    │   │   ├── coverage_state_test.go
    │   │   ├── keys_coverage_test.go
    │   │   ├── keys.go
    │   │   ├── openfor_test.go
    │   │   ├── overlay_coverage_extra_test.go
    │   │   ├── overlay.go
    │   │   ├── overlay_lifecycle_test.go
    │   │   ├── session.go
    │   │   ├── session_state_test.go
    │   │   ├── state_coverage_test.go
    │   │   ├── ttyx_events_test.go
    │   │   ├── ttyx_test.go
    │   │   ├── watch_coverage_test.go
    │   │   └── watch.go
    │   ├── unpack
    │   │   ├── coverage_test.go
    │   │   ├── formats.go
    │   │   ├── formats_lite.go
    │   │   ├── formats_lite_test.go
    │   │   ├── unpack_errors_test.go
    │   │   ├── unpack.go
    │   │   ├── unpack_sevenzip_test.go
    │   │   └── unpack_test.go
    │   ├── update
    │   │   ├── assets_test.go
    │   │   ├── backup.go
    │   │   ├── backup_test.go
    │   │   ├── channel_switch_test.go
    │   │   ├── cli.go
    │   │   ├── cli_test.go
    │   │   ├── coverage_test.go
    │   │   ├── edition.go
    │   │   ├── edition_lite.go
    │   │   ├── elevation_manual_windows_test.go
    │   │   ├── elevation_other.go
    │   │   ├── elevation_windows.go
    │   │   ├── helper_args.go
    │   │   ├── libc_default.go
    │   │   ├── libc_default_test.go
    │   │   ├── libc_musl.go
    │   │   ├── libc_musl_test.go
    │   │   ├── release_audit.go
    │   │   ├── release_audit_test.go
    │   │   ├── release_os_default.go
    │   │   ├── release_os.go
    │   │   ├── release_os_win7.go
    │   │   ├── selfexec.go
    │   │   ├── selfexec_linux.go
    │   │   ├── selfexec_linux_test.go
    │   │   ├── selfexec_other.go
    │   │   ├── selfexec_termux.go
    │   │   ├── selfexec_test.go
    │   │   ├── startcheck.go
    │   │   ├── startcheck_other.go
    │   │   ├── startcheck_test.go
    │   │   ├── startcheck_windows.go
    │   │   ├── update.go
    │   │   └── update_test.go
    │   ├── viewer
    │   │   ├── ansi.go
    │   │   ├── ansi_test.go
    │   │   ├── application.go
    │   │   ├── backend.go
    │   │   ├── backend_test.go
    │   │   ├── binary.go
    │   │   ├── colorizer.go
    │   │   ├── disasm.go
    │   │   ├── disasm_test.go
    │   │   ├── events.go
    │   │   ├── events_test.go
    │   │   ├── highlight.go
    │   │   ├── highlight_test.go
    │   │   ├── links.go
    │   │   ├── links_test.go
    │   │   ├── main_test.go
    │   │   ├── menubar_test.go
    │   │   ├── search_coverage_test.go
    │   │   ├── search.go
    │   │   ├── tail_test.go
    │   │   ├── text.go
    │   │   ├── text_test.go
    │   │   ├── title.go
    │   │   ├── topbar.go
    │   │   ├── topbar_test.go
    │   │   ├── view_coverage_extra_test.go
    │   │   ├── view.go
    │   │   ├── view_semantic_contract_test.go
    │   │   ├── view_semantic.go
    │   │   ├── view_test.go
    │   │   ├── wordnav.go
    │   │   └── wordnav_test.go
    │   ├── vtvibe
    │   │   ├── ap
    │   │   │   ├── apply.go
    │   │   │   ├── cases_test.go
    │   │   │   ├── crlf_test.go
    │   │   │   ├── dryrun_test.go
    │   │   │   ├── errors.go
    │   │   │   ├── fsutil.go
    │   │   │   ├── legacy_cases_test.go
    │   │   │   ├── modify.go
    │   │   │   ├── modresult_test.go
    │   │   │   ├── only_test.go
    │   │   │   ├── parse.go
    │   │   │   ├── preview.go
    │   │   │   ├── preview_test.go
    │   │   │   ├── report.go
    │   │   │   ├── run_tests_test.go
    │   │   │   ├── search.go
    │   │   │   ├── structure.go
    │   │   │   ├── testdata
    │   │   │   │   └── reftests
    │   │   │   │       ├── expected
    │   │   │   │       │   ├── 01_basic.cpp
    │   │   │   │       │   ├── 02_sequences.py
    │   │   │   │       │   ├── 03_tabs.py
    │   │   │   │       │   ├── 04_spaces.py
    │   │   │   │       │   ├── 05_crlf.txt
    │   │   │   │       │   ├── 06_short_anchor.py
    │   │   │   │       │   ├── 07_empty_lines.py
    │   │   │   │       │   ├── 14_edge_cases.py
    │   │   │   │       │   ├── 15_robustness.js
    │   │   │   │       │   ├── 18_idempotency.py
    │   │   │   │       │   ├── 19_idempotency_noop.py
    │   │   │   │       │   ├── 22_range_replace.py
    │   │   │   │       │   ├── 24_heuristics.py
    │   │   │   │       │   ├── 25_calculator.py
    │   │   │   │       │   ├── 26_implicit_create_file.txt
    │   │   │   │       │   ├── 27_anchor_resolution.py
    │   │   │   │       │   ├── 28_mixed_locators.py
    │   │   │   │       │   ├── 29_anchor_overlap.py
    │   │   │   │       │   ├── 30_locality_heuristic.py
    │   │   │   │       │   ├── 31_redundant_snippet.py
    │   │   │   │       │   ├── 32_intersection_resolution.py
    │   │   │   │       │   ├── 33_snippet_locality.py
    │   │   │   │       │   ├── 34_range_priority_strict.py
    │   │   │   │       │   ├── 37_heuristic_end_eq_content.py
    │   │   │   │       │   ├── 38_deep_scope.py
    │   │   │   │       │   ├── 39_sequential_repeats.py
    │   │   │   │       │   ├── 40_unified_snippet.py
    │   │   │   │       │   ├── 41_indent_trailing_newline.py
    │   │   │   │       │   ├── 43_created.txt
    │   │   │   │       │   ├── 49_sequential_cursor.py
    │   │   │   │       │   ├── 50_identical_snippet_tail.py
    │   │   │   │       │   ├── 52_atomic_src1.txt
    │   │   │   │       │   ├── 54_crlf.txt
    │   │   │   │       │   ├── 56_insert_noop.txt
    │   │   │   │       │   ├── 57_explicit_lf.txt
    │   │   │   │       │   ├── 58_explicit_cr.txt
    │   │   │   │       │   ├── 60_idempotent_create.txt
    │   │   │   │       │   ├── 67_created.txt
    │   │   │   │       │   ├── 68_source.py
    │   │   │   │       │   ├── 70_source.txt
    │   │   │   │       │   ├── 71_source.txt
    │   │   │   │       │   ├── 72_source.txt
    │   │   │   │       │   ├── 73_source.txt
    │   │   │   │       │   ├── 74_source.txt
    │   │   │   │       │   ├── 75_source.py
    │   │   │   │       │   └── new
    │   │   │   │       │       └── created_file.txt
    │   │   │   │       ├── patches
    │   │   │   │       │   ├── 01_basic_replace.ap
    │   │   │   │       │   ├── 02_sequences.ap
    │   │   │   │       │   ├── 03_tabs.ap
    │   │   │   │       │   ├── 04_spaces.ap
    │   │   │   │       │   ├── 05_crlf.ap
    │   │   │   │       │   ├── 06_short_anchor.ap
    │   │   │   │       │   ├── 07_empty_lines.ap
    │   │   │   │       │   ├── 08_error_snippet_not_found.ap
    │   │   │   │       │   ├── 09_error_anchor_not_found.ap
    │   │   │   │       │   ├── 10_error_ambiguous.ap
    │   │   │   │       │   ├── 11_error_invalid_header.ap
    │   │   │   │       │   ├── 12_error_invalid_spec.ap
    │   │   │   │       │   ├── 13_create_file.ap
    │   │   │   │       │   ├── 14_edge_cases.ap
    │   │   │   │       │   ├── 15_robustness.ap
    │   │   │   │       │   ├── 18_idempotency.ap
    │   │   │   │       │   ├── 19_idempotency_noop.ap
    │   │   │   │       │   ├── 21_error_atomic_failure.ap
    │   │   │   │       │   ├── 22_range_replace.ap
    │   │   │   │       │   ├── 23_error_range_ambiguous.ap
    │   │   │   │       │   ├── 24_heuristics.ap
    │   │   │   │       │   ├── 25_calculator_example.ap
    │   │   │   │       │   ├── 26_implicit_create_file.ap
    │   │   │   │       │   ├── 27_anchor_resolution.ap
    │   │   │   │       │   ├── 28_mixed_locators.ap
    │   │   │   │       │   ├── 29_anchor_overlap.ap
    │   │   │   │       │   ├── 30_locality_heuristic.ap
    │   │   │   │       │   ├── 31_redundant_snippet.ap
    │   │   │   │       │   ├── 32_intersection_resolution.ap
    │   │   │   │       │   ├── 33_snippet_locality.ap
    │   │   │   │       │   ├── 34_range_priority_strict.ap
    │   │   │   │       │   ├── 37_heuristic_end_eq_content.ap
    │   │   │   │       │   ├── 38_deep_scope.ap
    │   │   │   │       │   ├── 39_sequential_repeats.ap
    │   │   │   │       │   ├── 40_unified_snippet.ap
    │   │   │   │       │   ├── 41_indent_trailing_newline.ap
    │   │   │   │       │   ├── 42_strict_cursor.ap
    │   │   │   │       │   ├── 43_heuristic_implicit_create.ap
    │   │   │   │       │   ├── 49_sequential_cursor.ap
    │   │   │   │       │   ├── 50_identical_snippet_tail.ap
    │   │   │   │       │   ├── 52_force_success_part.ap
    │   │   │   │       │   ├── 53_force_fail_report.ap
    │   │   │   │       │   ├── 54_crlf_preservation.ap
    │   │   │   │       │   ├── 56_insert_noop.ap
    │   │   │   │       │   ├── 57_explicit_lf.ap
    │   │   │   │       │   ├── 58_explicit_cr.ap
    │   │   │   │       │   ├── 60_idempotent_create.ap
    │   │   │   │       │   ├── 67_actions_after_create.ap
    │   │   │   │       │   ├── 68_idempotency_cursor_desync.ap
    │   │   │   │       │   ├── 69_delete_snippet_tail_not_found.ap
    │   │   │   │       │   ├── 70_recreate_file.ap
    │   │   │   │       │   ├── 71_recreate_idempotency.ap
    │   │   │   │       │   ├── 72_boundary_anchors_range.ap
    │   │   │   │       │   ├── 73_boundary_anchors_single.ap
    │   │   │   │       │   ├── 74_robust_overlap.ap
    │   │   │   │       │   └── 75_multipass_retry.ap
    │   │   │   │       └── src
    │   │   │   │           ├── 01_basic.cpp
    │   │   │   │           ├── 02_sequences.py
    │   │   │   │           ├── 03_tabs.py
    │   │   │   │           ├── 04_spaces.py
    │   │   │   │           ├── 05_crlf.txt
    │   │   │   │           ├── 06_short_anchor.py
    │   │   │   │           ├── 07_empty_lines.py
    │   │   │   │           ├── 08_error_src.py
    │   │   │   │           ├── 09_error_src.py
    │   │   │   │           ├── 10_error_src.py
    │   │   │   │           ├── 14_edge_cases.py
    │   │   │   │           ├── 15_robustness.js
    │   │   │   │           ├── 18_idempotency.py
    │   │   │   │           ├── 19_idempotency_noop.py
    │   │   │   │           ├── 21_atomic_src1.txt
    │   │   │   │           ├── 21_atomic_src2.txt
    │   │   │   │           ├── 22_range_replace.py
    │   │   │   │           ├── 23_error_range_ambiguous.py
    │   │   │   │           ├── 24_heuristics.py
    │   │   │   │           ├── 25_calculator.py
    │   │   │   │           ├── 27_anchor_resolution.py
    │   │   │   │           ├── 28_mixed_locators.py
    │   │   │   │           ├── 29_anchor_overlap.py
    │   │   │   │           ├── 30_locality_heuristic.py
    │   │   │   │           ├── 31_redundant_snippet.py
    │   │   │   │           ├── 32_intersection_resolution.py
    │   │   │   │           ├── 33_snippet_locality.py
    │   │   │   │           ├── 34_range_priority_strict.py
    │   │   │   │           ├── 37_heuristic_end_eq_content.py
    │   │   │   │           ├── 38_deep_scope.py
    │   │   │   │           ├── 39_sequential_repeats.py
    │   │   │   │           ├── 40_unified_snippet.py
    │   │   │   │           ├── 41_indent_trailing_newline.py
    │   │   │   │           ├── 42_strict_cursor.py
    │   │   │   │           ├── 49_sequential_cursor.py
    │   │   │   │           ├── 50_identical_snippet_tail.py
    │   │   │   │           ├── 52_atomic_src1.txt
    │   │   │   │           ├── 52_atomic_src2.txt
    │   │   │   │           ├── 54_crlf.txt
    │   │   │   │           ├── 56_insert_noop.txt
    │   │   │   │           ├── 57_explicit_lf.txt
    │   │   │   │           ├── 58_explicit_cr.txt
    │   │   │   │           ├── 60_idempotent_create.txt
    │   │   │   │           ├── 68_source.py
    │   │   │   │           ├── 69_source.py
    │   │   │   │           ├── 70_source.txt
    │   │   │   │           ├── 71_source.txt
    │   │   │   │           ├── 72_source.txt
    │   │   │   │           ├── 73_source.txt
    │   │   │   │           ├── 74_source.txt
    │   │   │   │           ├── 75_source.py
    │   │   │   │           └── dummy.txt
    │   │   │   ├── types.go
    │   │   │   ├── undo_disk.go
    │   │   │   ├── undo.go
    │   │   │   ├── undo_test.go
    │   │   │   └── util.go
    │   │   ├── ap.go
    │   │   ├── ap_test.go
    │   │   ├── coverage_test.go
    │   │   ├── defaults.go
    │   │   ├── draft.go
    │   │   ├── draft_test.go
    │   │   ├── memtree.go
    │   │   ├── pack.go
    │   │   ├── pack_test.go
    │   │   ├── provider.go
    │   │   ├── provider_test.go
    │   │   ├── session.go
    │   │   ├── session_test.go
    │   │   ├── vfs_coverage_test.go
    │   │   └── vfs.go
    │   ├── wheel
    │   │   ├── wheel.go
    │   │   └── wheel_test.go
    │   ├── wincon
    │   │   ├── blit_test.go
    │   │   ├── coverage_test.go
    │   │   ├── geometry.go
    │   │   ├── layered.go
    │   │   ├── layered_test.go
    │   │   ├── overlay_other.go
    │   │   ├── overlay_state.go
    │   │   ├── overlay_state_test.go
    │   │   ├── overlay_windows.go
    │   │   ├── overlay_windows_test.go
    │   │   ├── stats.go
    │   │   ├── stats_windows.go
    │   │   ├── wincon_test.go
    │   │   ├── windowlongptr_32.go
    │   │   └── windowlongptr_64.go
    │   ├── wincondrag
    │   │   ├── backend_other.go
    │   │   ├── backend_windows.go
    │   │   ├── doc.go
    │   │   ├── logic.go
    │   │   └── logic_test.go
    │   └── winex11drag
    │       ├── display.go
    │       ├── display_test.go
    │       ├── doc.go
    │       ├── wineconn.go
    │       ├── wineconn_test.go
    │       ├── x11window.go
    │       ├── x11window_test.go
    │       ├── x11window_windows.go
    │       ├── xauthority.go
    │       └── xauthority_test.go
    ├── LICENSE
    ├── .mcp.json
    ├── packaging
    │   ├── linux
    │   │   └── org.unxed.f4.desktop
    │   ├── macos
    │   │   └── Info.plist
    │   ├── nix
    │   │   ├── f4-ini-upsert.sh
    │   │   ├── home-manager-module-check.nix
    │   │   ├── home-manager-module.nix
    │   │   └── ini.nix
    │   ├── openwrt
    │   │   └── f4
    │   │       └── Makefile
    │   └── termux
    │       └── build.sh
    ├── plugins
    │   ├── android
    │   │   ├── adb_integration_test.go
    │   │   ├── adb_sync.go
    │   │   ├── adb_sync_test.go
    │   │   ├── adb_transport.go
    │   │   ├── adb_transport_test.go
    │   │   ├── adb_wire_test.go
    │   │   ├── cmd
    │   │   │   └── android-plugin
    │   │   │       ├── main.go
    │   │   │       └── plugring-manifest.json
    │   │   ├── command_runner_info_test.go
    │   │   ├── coverage_contract_test.go
    │   │   ├── device.go
    │   │   ├── device_test.go
    │   │   ├── fish_pool.go
    │   │   ├── fish_pool_test.go
    │   │   ├── go.mod
    │   │   ├── info.go
    │   │   ├── info_test.go
    │   │   ├── manager.go
    │   │   ├── manager_test.go
    │   │   ├── pathutil.go
    │   │   ├── pathutil_test.go
    │   │   ├── README.md
    │   │   ├── rpc_plugin.go
    │   │   ├── sync_vfs.go
    │   │   └── sync_vfs_test.go
    │   ├── archive
    │   │   ├── archive.go
    │   │   ├── archive_materialize_unix_test.go
    │   │   ├── archive_plugin_test.go
    │   │   ├── archive_test.go
    │   │   ├── archive_write_regression_test.go
    │   │   ├── clone_test.go
    │   │   ├── compressed_regular_test.go
    │   │   ├── doc.go
    │   │   ├── enabled_test.go
    │   │   ├── extraction_paths_test.go
    │   │   ├── extraction_security_test.go
    │   │   ├── gzip_view.go
    │   │   ├── gzip_view_test.go
    │   │   ├── issue1179_sfx_test.go
    │   │   ├── issue1179_volumes_test.go
    │   │   ├── issue1184_test.go
    │   │   ├── issue1186_sfx_zip_test.go
    │   │   ├── issue1186_split_zip_test.go
    │   │   ├── issue1186_winrar_aes_test.go
    │   │   ├── issue1187_test.go
    │   │   ├── issue1243_test.go
    │   │   ├── issue1250_marked_test.go
    │   │   ├── issue1250_prompt_test.go
    │   │   ├── issue1250_rar_test.go
    │   │   ├── issue1250_test.go
    │   │   ├── issue1272_test.go
    │   │   ├── issue1383_test.go
    │   │   ├── issue1504_test.go
    │   │   ├── issue815_f3_test.go
    │   │   ├── issue816_multivolume_test.go
    │   │   ├── issue816_password_retry_test.go
    │   │   ├── issue915_total_progress_test.go
    │   │   ├── materialize_coverage_test.go
    │   │   ├── materialize.go
    │   │   ├── multivolume_rar.go
    │   │   ├── nested_detection_test.go
    │   │   ├── nested_matrix_test.go
    │   │   ├── password_coverage_test.go
    │   │   ├── password.go
    │   │   ├── password_test.go
    │   │   ├── production_regression_test.go
    │   │   ├── provider.go
    │   │   ├── provider_special_unix_test.go
    │   │   ├── provider_test.go
    │   │   ├── rar_password.go
    │   │   ├── ratarmount.go
    │   │   ├── ratarmount_test.go
    │   │   ├── repro_test.go
    │   │   ├── seq_reader.go
    │   │   ├── seq_reader_test.go
    │   │   ├── sfx.go
    │   │   ├── sfx_test.go
    │   │   ├── stream.go
    │   │   ├── stream_test.go
    │   │   ├── tar_index_cache_option_test.go
    │   │   ├── tar_index.go
    │   │   ├── testdata
    │   │   │   └── issue1186_winrar_aes_sfx.exe
    │   │   ├── title.go
    │   │   ├── vfs_coverage_batch44_test.go
    │   │   ├── vfs_coverage_extra_test.go
    │   │   ├── vfs.go
    │   │   ├── vfs_nested_test.go
    │   │   ├── vfs_test.go
    │   │   ├── zipcrypto_checkbyte_test.go
    │   │   ├── zip_encoding.go
    │   │   └── zip_encoding_test.go
    │   ├── chroma
    │   │   ├── chroma.go
    │   │   └── chroma_test.go
    │   ├── cloudfox
    │   │   ├── cloud_vfs_coverage_test.go
    │   │   ├── cloud_vfs.go
    │   │   ├── cloud_vfs_share_test.go
    │   │   ├── cloud_vfs_test.go
    │   │   ├── cmd
    │   │   │   └── cloudfox-plugin
    │   │   │       ├── main.go
    │   │   │       └── plugring-manifest.json
    │   │   ├── context_editor_test.go
    │   │   ├── credential_scope.go
    │   │   ├── credential_scope_test.go
    │   │   ├── dialog_coverage_extra_test.go
    │   │   ├── dialog.go
    │   │   ├── dialog_google_test.go
    │   │   ├── dialog_s3_test.go
    │   │   ├── go.mod
    │   │   ├── manager_coverage_extra_test.go
    │   │   ├── manager.go
    │   │   ├── oauth.go
    │   │   ├── password_prompt.go
    │   │   ├── password_prompt_test.go
    │   │   ├── plugin_contributions_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── provider_capabilities_test.go
    │   │   ├── provider_google.go
    │   │   ├── provider_google_production_test.go
    │   │   ├── provider_google_real_native_integration_test.go
    │   │   ├── provider_google_share.go
    │   │   ├── provider_google_share_test.go
    │   │   ├── provider_google_test.go
    │   │   ├── provider_helpers.go
    │   │   ├── provider_helpers_test.go
    │   │   ├── provider_mutation_test.go
    │   │   ├── provider_real_diagnostics_test.go
    │   │   ├── provider_real_saved_integration_test.go
    │   │   ├── provider_real_semantics_test.go
    │   │   ├── provider_real_sharing_integration_test.go
    │   │   ├── provider_real_upload_cancellation_test.go
    │   │   ├── provider_s3_discovery_core_test.go
    │   │   ├── provider_s3_discovery_regression_test.go
    │   │   ├── provider_s3.go
    │   │   ├── provider_s3_real_discovery_test.go
    │   │   ├── provider_s3_share.go
    │   │   ├── provider_s3_share_test.go
    │   │   ├── provider_s3_test.go
    │   │   ├── provider_webdav_edge_test.go
    │   │   ├── provider_webdav.go
    │   │   ├── provider_webdav_integration_test.go
    │   │   ├── provider_webdav_share.go
    │   │   ├── provider_webdav_share_test.go
    │   │   ├── provider_webdav_test.go
    │   │   ├── provider_yandex_cache.go
    │   │   ├── provider_yandex.go
    │   │   ├── provider_yandex_info.go
    │   │   ├── provider_yandex_production_test.go
    │   │   ├── provider_yandex_share.go
    │   │   ├── provider_yandex_share_test.go
    │   │   ├── provider_yandex_test.go
    │   │   ├── rpc_plugin.go
    │   │   ├── secrets.go
    │   │   ├── secrets_test.go
    │   │   ├── session.go
    │   │   ├── settings_center_draft_test.go
    │   │   ├── settings_center.go
    │   │   ├── settings_center_test.go
    │   │   ├── settings_russian_test.go
    │   │   ├── store.go
    │   │   ├── store_lock_unix.go
    │   │   ├── store_lock_windows.go
    │   │   ├── store_test.go
    │   │   ├── test_main_test.go
    │   │   ├── types.go
    │   │   ├── uri.go
    │   │   ├── uri_test.go
    │   │   ├── vault.go
    │   │   ├── yandex_code_prompt_coverage_test.go
    │   │   └── yandex_code_prompt.go
    │   ├── dockerfs
    │   │   ├── client.go
    │   │   ├── contexts.go
    │   │   ├── contexts_test.go
    │   │   ├── dockerfs_test.go
    │   │   ├── endpoint.go
    │   │   ├── endpoint_test.go
    │   │   ├── locale.go
    │   │   ├── pipe.go
    │   │   ├── pipe_windows_test.go
    │   │   ├── plugin.go
    │   │   ├── uri.go
    │   │   └── vfs.go
    │   ├── dotnet
    │   │   ├── dotnet_test.go
    │   │   ├── plugin.go
    │   │   ├── provider.go
    │   │   └── vfs.go
    │   ├── dummy_internal
    │   │   ├── dummy_internal.go
    │   │   └── dummy_internal_test.go
    │   ├── dummy_lua
    │   │   ├── plugin.lua
    │   │   └── README.md
    │   ├── dummy_rpc
    │   │   ├── main.go
    │   │   ├── main_test.go
    │   │   └── panel_keys_test.go
    │   ├── envman
    │   │   ├── codec.go
    │   │   ├── codec_test.go
    │   │   ├── commands.go
    │   │   ├── commands_test.go
    │   │   ├── dialogs.go
    │   │   ├── environment_document.go
    │   │   ├── far3_import.go
    │   │   ├── far3_import_other.go
    │   │   ├── far3_import_test.go
    │   │   ├── far3_import_ui.go
    │   │   ├── far3_import_windows.go
    │   │   ├── far3_import_windows_test.go
    │   │   ├── help.go
    │   │   ├── help_test.go
    │   │   ├── manager_frame.go
    │   │   ├── manager_ops.go
    │   │   ├── manager_ops_test.go
    │   │   ├── manager_ui.go
    │   │   ├── messages.go
    │   │   ├── model.go
    │   │   ├── model_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── README.md
    │   │   ├── settings_center_coverage_test.go
    │   │   ├── settings_center.go
    │   │   ├── settings.go
    │   │   ├── settings_russian_test.go
    │   │   ├── settings_test.go
    │   │   ├── strings.go
    │   │   ├── ui_test.go
    │   │   ├── vfs_io.go
    │   │   └── vfs_io_test.go
    │   ├── git
    │   │   ├── branch.go
    │   │   ├── branch_test.go
    │   │   ├── branchview.go
    │   │   ├── branchview_test.go
    │   │   ├── commit.go
    │   │   ├── commit_test.go
    │   │   ├── diff.go
    │   │   ├── diff_test.go
    │   │   ├── help.go
    │   │   ├── hunk_deleted_test.go
    │   │   ├── hunk_discard_test.go
    │   │   ├── hunk.go
    │   │   ├── hunk_new_test.go
    │   │   ├── hunk_test.go
    │   │   ├── hunkview.go
    │   │   ├── logdifffiles.go
    │   │   ├── logdifffiles_test.go
    │   │   ├── logdiff.go
    │   │   ├── logdiff_test.go
    │   │   ├── log.go
    │   │   ├── log_test.go
    │   │   ├── logview.go
    │   │   ├── logview_test.go
    │   │   ├── main_test.go
    │   │   ├── panel.go
    │   │   ├── panel_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── README.md
    │   │   ├── remote.go
    │   │   ├── stage.go
    │   │   ├── stage_test.go
    │   │   ├── status.go
    │   │   ├── status_test.go
    │   │   ├── untracked_dir.go
    │   │   ├── untracked_dir_test.go
    │   │   ├── untracked.go
    │   │   └── untracked_test.go
    │   ├── id3editor
    │   │   ├── plugin_contributions_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_handle_test.go
    │   │   ├── plugin_paths_test.go
    │   │   └── plugin_test.go
    │   ├── ide
    │   │   ├── plugin.go
    │   │   └── plugin_test.go
    │   ├── intchecker
    │   │   ├── display.go
    │   │   ├── display_test.go
    │   │   ├── encoding.go
    │   │   ├── encoding_test.go
    │   │   ├── generate.go
    │   │   ├── generate_test.go
    │   │   ├── hashfile.go
    │   │   ├── hashfile_test.go
    │   │   ├── options_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── progress.go
    │   │   ├── progress_test.go
    │   │   ├── settings.go
    │   │   ├── settings_test.go
    │   │   ├── validate.go
    │   │   ├── validate_test.go
    │   │   └── validate_ui.go
    │   ├── ios
    │   │   ├── afc_registry_test.go
    │   │   ├── afc_vfs_contract_test.go
    │   │   ├── afc_vfs.go
    │   │   ├── afc_vfs_live_test.go
    │   │   ├── afc_vfs_test.go
    │   │   ├── apps.go
    │   │   ├── cmd
    │   │   │   └── ios-plugin
    │   │   │       ├── main.go
    │   │   │       └── plugring-manifest.json
    │   │   ├── core_access_coverage_test.go
    │   │   ├── core_access.go
    │   │   ├── core_access_stub.go
    │   │   ├── core_access_supported.go
    │   │   ├── core_access_supported_test.go
    │   │   ├── core_tunnel_supported.go
    │   │   ├── core_vfs.go
    │   │   ├── core_vfs_test.go
    │   │   ├── coverage_edges_test.go
    │   │   ├── coverage_plugins_test.go
    │   │   ├── coverage_test.go
    │   │   ├── go.mod
    │   │   ├── internal
    │   │   │   ├── afcproto
    │   │   │   │   ├── client.go
    │   │   │   │   ├── client_test.go
    │   │   │   │   ├── doc.go
    │   │   │   │   ├── errors.go
    │   │   │   │   ├── file.go
    │   │   │   │   ├── path.go
    │   │   │   │   ├── protocol.go
    │   │   │   │   ├── protocol_test.go
    │   │   │   │   └── types.go
    │   │   │   └── corefileservice
    │   │   │       ├── doc.go
    │   │   │       ├── fileservice.go
    │   │   │       └── fileservice_test.go
    │   │   ├── ios_integration_test.go
    │   │   ├── LICENSE.go-ios
    │   │   ├── manager.go
    │   │   ├── manager_test.go
    │   │   ├── native_source.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── README.md
    │   │   ├── rpc_plugin.go
    │   │   ├── selectors.go
    │   │   ├── selectors_test.go
    │   │   ├── services.go
    │   │   └── services_test.go
    │   ├── k8sfs
    │   │   ├── client.go
    │   │   ├── contexts.go
    │   │   ├── contexts_test.go
    │   │   ├── k8sfs_test.go
    │   │   ├── kubeconfig.go
    │   │   ├── locale.go
    │   │   ├── plugin.go
    │   │   ├── uri.go
    │   │   └── vfs.go
    │   ├── mediainfo
    │   │   ├── analyzer.go
    │   │   ├── backend_test.go
    │   │   ├── cache.go
    │   │   ├── cache_test.go
    │   │   ├── config_dialog.go
    │   │   ├── config_dialog_test.go
    │   │   ├── coverage_contract_more_test.go
    │   │   ├── coverage_test.go
    │   │   ├── dialog.go
    │   │   ├── dialog_theme_test.go
    │   │   ├── dialog_util_coverage_test.go
    │   │   ├── exif_report_test.go
    │   │   ├── format_gaps_test.go
    │   │   ├── locale.go
    │   │   ├── macro.go
    │   │   ├── macro_test.go
    │   │   ├── matroska_bounds_test.go
    │   │   ├── model.go
    │   │   ├── open_coverage_test.go
    │   │   ├── open_enabled_test.go
    │   │   ├── open.go
    │   │   ├── open_test.go
    │   │   ├── parse_audio_containers_test.go
    │   │   ├── parse_audio_coverage_batch20_test.go
    │   │   ├── parse_audio.go
    │   │   ├── parse_ebu_stl.go
    │   │   ├── parse_heif.go
    │   │   ├── parse_image_coverage_test.go
    │   │   ├── parse_image.go
    │   │   ├── parse_iso_coverage_test.go
    │   │   ├── parse_iso.go
    │   │   ├── parse_matroska.go
    │   │   ├── parse_riff.go
    │   │   ├── parse_subtitle.go
    │   │   ├── parse_tiff.go
    │   │   ├── parse_tiff_limits_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_language_switch_test.go
    │   │   ├── plugin_test.go
    │   │   ├── quickview_provider.go
    │   │   ├── quickview_provider_test.go
    │   │   ├── README.md
    │   │   ├── render.go
    │   │   ├── render_limits_test.go
    │   │   ├── report_text.go
    │   │   ├── report_view.go
    │   │   ├── settings_center.go
    │   │   ├── settings.go
    │   │   ├── settings_russian_test.go
    │   │   ├── settings_test.go
    │   │   ├── source.go
    │   │   ├── subtitle_limits_test.go
    │   │   └── util.go
    │   ├── mongofs
    │   │   ├── bson.go
    │   │   ├── conn.go
    │   │   ├── ejson.go
    │   │   ├── jsonutil.go
    │   │   ├── locale.go
    │   │   ├── mongofs_test.go
    │   │   ├── plugin.go
    │   │   ├── uri.go
    │   │   └── vfs.go
    │   ├── multiarc
    │   │   ├── backend_7z_gap_test.go
    │   │   ├── backend_7z.go
    │   │   ├── backend_7z_realexec_test.go
    │   │   ├── backend_7z_test.go
    │   │   ├── backend_7z_write.go
    │   │   ├── backend_7z_write_test.go
    │   │   ├── backend_gzip.go
    │   │   ├── backend_gzip_realexec_test.go
    │   │   ├── backend_gzip_test.go
    │   │   ├── backend_gzip_write.go
    │   │   ├── backend_gzip_write_test.go
    │   │   ├── backend_tar.go
    │   │   ├── backend_tar_realexec_test.go
    │   │   ├── backend_tar_test.go
    │   │   ├── backend_tar_write.go
    │   │   ├── backend_tar_write_test.go
    │   │   ├── backend_zip.go
    │   │   ├── backend_zip_realexec_test.go
    │   │   ├── backend_zip_test.go
    │   │   ├── backend_zip_write.go
    │   │   ├── backend_zip_write_test.go
    │   │   ├── create_command.go
    │   │   ├── create_command_test.go
    │   │   ├── create.go
    │   │   ├── create_realexec_test.go
    │   │   ├── create_test.go
    │   │   ├── fake_archiver_test.go
    │   │   ├── format.go
    │   │   ├── format_test.go
    │   │   ├── multiarc.go
    │   │   ├── provider_gap_test.go
    │   │   ├── provider.go
    │   │   ├── provider_test.go
    │   │   ├── realexec_test.go
    │   │   ├── tools.go
    │   │   ├── tools_realexec_test.go
    │   │   ├── tools_test.go
    │   │   ├── vfs_gap_test.go
    │   │   ├── vfs.go
    │   │   ├── vfs_test.go
    │   │   ├── vfs_write_gap_test.go
    │   │   ├── vfs_write.go
    │   │   ├── vfs_write_test.go
    │   │   ├── write.go
    │   │   └── write_test.go
    │   ├── netbrowse
    │   │   ├── doc.go
    │   │   ├── enum_other.go
    │   │   ├── enum_windows.go
    │   │   ├── enum_windows_test.go
    │   │   ├── main_test.go
    │   │   ├── mdns.go
    │   │   ├── mdns_test.go
    │   │   ├── netbrowse_test.go
    │   │   ├── netvfs.go
    │   │   ├── netvfs_test.go
    │   │   ├── panel.go
    │   │   ├── plugin.go
    │   │   ├── resource.go
    │   │   ├── smb_full.go
    │   │   ├── smb_lite.go
    │   │   ├── smbnet.go
    │   │   ├── smbnet_test.go
    │   │   └── uri.go
    │   ├── netfox
    │   │   ├── context_editor_test.go
    │   │   ├── coverage_test.go
    │   │   ├── crypto.go
    │   │   ├── crypto_test.go
    │   │   ├── dev
    │   │   │   ├── README.md
    │   │   │   └── unxed_f4_issue_316.json
    │   │   ├── dialog.go
    │   │   ├── dialog_test.go
    │   │   ├── fish_clone_session_test.go
    │   │   ├── fish_dialer_lite.go
    │   │   ├── fish_dialer_lite_test.go
    │   │   ├── fish_dialer_test.go
    │   │   ├── fish_native.go
    │   │   ├── fish_native_test.go
    │   │   ├── fishplus
    │   │   │   ├── cancel_test.go
    │   │   │   ├── cand
    │   │   │   ├── exec.go
    │   │   │   ├── exec_test.go
    │   │   │   ├── fs.go
    │   │   │   ├── fs_test.go
    │   │   │   ├── hash.go
    │   │   │   ├── hash_test.go
    │   │   │   ├── helper.ps1
    │   │   │   ├── helper.sh
    │   │   │   ├── job.go
    │   │   │   ├── job_test.go
    │   │   │   ├── keepalive.go
    │   │   │   ├── keepalive_test.go
    │   │   │   ├── ls.go
    │   │   │   ├── ls_test.go
    │   │   │   ├── mutate.go
    │   │   │   ├── mutate_test.go
    │   │   │   ├── patch.go
    │   │   │   ├── patch_test.go
    │   │   │   ├── paths.go
    │   │   │   ├── paths_test.go
    │   │   │   ├── random_fixture_test.go
    │   │   │   ├── read.go
    │   │   │   ├── read_test.go
    │   │   │   ├── script.go
    │   │   │   ├── script_pwsh_test.go
    │   │   │   ├── script_test.go
    │   │   │   ├── search.go
    │   │   │   ├── search_test.go
    │   │   │   ├── server_fs.go
    │   │   │   ├── server.go
    │   │   │   ├── server_mutate.go
    │   │   │   ├── server_patch.go
    │   │   │   ├── server_stat_other.go
    │   │   │   ├── server_stat_unix.go
    │   │   │   ├── server_test.go
    │   │   │   ├── session.go
    │   │   │   ├── session_pwsh_test.go
    │   │   │   ├── session_test.go
    │   │   │   ├── sizes
    │   │   │   ├── WINDOWS_PORT.md
    │   │   │   ├── write.go
    │   │   │   └── write_test.go
    │   │   ├── fish_pool_coverage_test.go
    │   │   ├── fish_pool.go
    │   │   ├── fish_reconnect_entry_test.go
    │   │   ├── fish_reconnect_test.go
    │   │   ├── fish_vfs.go
    │   │   ├── fish_vfs_test.go
    │   │   ├── ftp_clone_test.go
    │   │   ├── ftp_coverage_batch41_test.go
    │   │   ├── ftp_vfs_contract_test.go
    │   │   ├── ftp_vfs.go
    │   │   ├── history.go
    │   │   ├── history_test.go
    │   │   ├── issue419_test.go
    │   │   ├── lang_test.go
    │   │   ├── netfox.go
    │   │   ├── netfox_test.go
    │   │   ├── netfox_uri_full.go
    │   │   ├── netfox_uri_lite.go
    │   │   ├── plugin_contributions_test.go
    │   │   ├── proxy_dialog_coverage_test.go
    │   │   ├── proxy_dialog.go
    │   │   ├── registry.go
    │   │   ├── s2s_password.go
    │   │   ├── s2s_password_test.go
    │   │   ├── settings_center.go
    │   │   ├── settings_center_test.go
    │   │   ├── settings_russian_test.go
    │   │   ├── sftp_command_coverage_batch40_test.go
    │   │   ├── sftp_command_test.go
    │   │   ├── sftp_coverage_batch30_test.go
    │   │   ├── sftp_dial_test.go
    │   │   ├── sftp_readat_test.go
    │   │   ├── sftp_rename_overwrite_test.go
    │   │   ├── sftp_uri_coverage_batch33_test.go
    │   │   ├── sftp_uri.go
    │   │   ├── sftp_vfs_coverage_batch31_test.go
    │   │   ├── sftp_vfs_coverage_extra_test.go
    │   │   ├── sftp_vfs_gap_test.go
    │   │   ├── sftp_vfs.go
    │   │   ├── smb_backend.go
    │   │   ├── smb_live_test.go
    │   │   ├── smb_provider.go
    │   │   ├── smb_uri.go
    │   │   ├── smb_vfs.go
    │   │   ├── smb_vfs_test.go
    │   │   ├── ssh_agent_forwarding_test.go
    │   │   ├── ssh_agent_unix.go
    │   │   ├── ssh_agent_windows.go
    │   │   ├── ssh_agent_windows_test.go
    │   │   ├── ssh_dial.go
    │   │   ├── ssh_dial_test.go
    │   │   ├── ssh_fish_dialer.go
    │   │   ├── ssh_keepalive_test.go
    │   │   ├── ssh_known_hosts_coverage_test.go
    │   │   ├── ssh_known_hosts.go
    │   │   ├── ssh_known_hosts_test.go
    │   │   ├── ssh_pty.go
    │   │   ├── vfs_abs_test.go
    │   │   ├── vfs.go
    │   │   ├── wsl_dialer_windows.go
    │   │   ├── wsl_dialer_windows_test.go
    │   │   ├── wsl_vfs_windows.go
    │   │   └── wsl_vfs_windows_test.go
    │   ├── observer
    │   │   ├── abi.go
    │   │   ├── config.go
    │   │   ├── config_test.go
    │   │   ├── doc.go
    │   │   ├── errors.go
    │   │   ├── fsbridge.go
    │   │   ├── growing_file.go
    │   │   ├── growing_file_test.go
    │   │   ├── hostimports.go
    │   │   ├── isoimg_e2e_test.go
    │   │   ├── observer_test.go
    │   │   ├── panel_enter_test.go
    │   │   ├── password.go
    │   │   ├── password_test.go
    │   │   ├── plugin.go
    │   │   ├── provider_config_test.go
    │   │   ├── provider.go
    │   │   ├── provider_test.go
    │   │   ├── runtime.go
    │   │   ├── testdata
    │   │   │   ├── isoimg
    │   │   │   │   └── compat
    │   │   │   │       ├── isz_stub.cpp
    │   │   │   │       ├── StdAfx.h
    │   │   │   │       ├── trampolines.cpp
    │   │   │   │       └── windows.h
    │   │   │   └── stub
    │   │   │       └── observer_stub.c
    │   │   ├── vfs.go
    │   │   └── wchar.go
    │   ├── pdfview
    │   │   ├── pdfview_test.go
    │   │   ├── plugin.go
    │   │   ├── provider.go
    │   │   └── vfs.go
    │   ├── proclist
    │   │   ├── actions.go
    │   │   ├── actions_test.go
    │   │   ├── actions_unix.go
    │   │   ├── actions_unix_test.go
    │   │   ├── actions_windows.go
    │   │   ├── actions_windows_test.go
    │   │   ├── collector_darwin.go
    │   │   ├── collector_darwin_test.go
    │   │   ├── collector.go
    │   │   ├── collector_linux.go
    │   │   ├── collector_linux_test.go
    │   │   ├── collector_other.go
    │   │   ├── collector_windows.go
    │   │   ├── collector_windows_test.go
    │   │   ├── columns_sync_test.go
    │   │   ├── config_dialog.go
    │   │   ├── config_dialog_test.go
    │   │   ├── details_darwin.go
    │   │   ├── details_darwin_test.go
    │   │   ├── details_environ.go
    │   │   ├── details_environ_test.go
    │   │   ├── details.go
    │   │   ├── details_linux.go
    │   │   ├── details_linux_test.go
    │   │   ├── details_test.go
    │   │   ├── details_windows.go
    │   │   ├── details_windows_test.go
    │   │   ├── main_test.go
    │   │   ├── panel.go
    │   │   ├── panel_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── priority_raw_darwin.go
    │   │   ├── priority_raw_linux.go
    │   │   ├── README.md
    │   │   ├── settings.go
    │   │   └── settings_test.go
    │   ├── sqlite
    │   │   ├── backend_cli.go
    │   │   ├── backend_cli_parse_test.go
    │   │   ├── backend_cli_test.go
    │   │   ├── backend_default_lite.go
    │   │   ├── backend_driver.go
    │   │   ├── coverage_test.go
    │   │   ├── export.go
    │   │   ├── locale.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── provider.go
    │   │   ├── ui.go
    │   │   ├── ui_test.go
    │   │   ├── vfs.go
    │   │   └── vfs_test.go
    │   ├── svcmgr
    │   │   ├── collector_other.go
    │   │   ├── collector_windows.go
    │   │   ├── collector_windows_test.go
    │   │   ├── control.go
    │   │   ├── control_other.go
    │   │   ├── control_windows.go
    │   │   ├── details.go
    │   │   ├── details_other.go
    │   │   ├── details_windows.go
    │   │   ├── doc.go
    │   │   ├── main_test.go
    │   │   ├── panel.go
    │   │   ├── plugin.go
    │   │   ├── properties.go
    │   │   ├── service.go
    │   │   └── svcmgr_test.go
    │   └── visren
    │       ├── config.go
    │       ├── config_test.go
    │       ├── coverage_contract_test.go
    │       ├── coverage_test.go
    │       ├── dialog_coverage_extra_test.go
    │       ├── dialog.go
    │       ├── dialog_test.go
    │       ├── editor.go
    │       ├── editor_test.go
    │       ├── engine_test.go
    │       ├── LICENSE.upstream
    │       ├── masks_coverage_test.go
    │       ├── masks.go
    │       ├── metadata_coverage_test.go
    │       ├── metadata.go
    │       ├── metadata_test.go
    │       ├── model.go
    │       ├── plugin.go
    │       ├── plugin_language_switch_test.go
    │       ├── plugin_test.go
    │       ├── rename.go
    │       ├── rename_test.go
    │       ├── replace.go
    │       ├── settings_center.go
    │       ├── settings_center_test.go
    │       ├── settings_russian_test.go
    │       ├── transforms.go
    │       └── word_div_prompt_test.go
    ├── plugring
    │   ├── hello_plugring.lua
    │   └── index.yaml
    ├── README.md
    ├── scripts
    │   ├── build_ipk.sh
    │   ├── build_isoimg_test_iso.sh
    │   ├── build_isoimg_test_wasm.sh
    │   ├── build_observer_test_wasm.sh
    │   ├── check_archive_deps.sh
    │   ├── check_isoimg_wasm.sh
    │   ├── check_release_version.sh
    │   ├── filelist_update.sh
    │   ├── import_mc_theme.py
    │   ├── openwrt_smoke.py
    │   ├── test_plugins.sh
    │   └── test_resurrect.sh
    ├── sdk
    │   ├── extui
    │   │   ├── model_coverage_test.go
    │   │   ├── model.go
    │   │   └── model_test.go
    │   ├── f4plugin
    │   │   ├── metadata.go
    │   │   ├── plugin.go
    │   │   └── plugin_test.go
    │   ├── f4rpc
    │   │   ├── mux.go
    │   │   └── mux_test.go
    │   ├── f4settings
    │   │   ├── localization_test.go
    │   │   ├── settings.go
    │   │   ├── settings_test.go
    │   │   └── struct_provider.go
    │   └── lua
    │       └── f4rpc.lua
    ├── skills-lock.json
    ├── tools
    │   ├── conpty_probe_child.py
    │   ├── conpty_probe.py
    │   ├── conptyreconcile
    │   │   ├── capture.go
    │   │   ├── clear_probe_windows.go
    │   │   ├── command_compare_windows.go
    │   │   ├── command_probe_windows.go
    │   │   ├── command_suite_windows.go
    │   │   ├── command_timing_windows.go
    │   │   ├── control_stream.go
    │   │   ├── control_stream_test.go
    │   │   ├── edge_probe_windows.go
    │   │   ├── emitter.go
    │   │   ├── empty_probe_windows.go
    │   │   ├── gate.go
    │   │   ├── gate_nonwindows.go
    │   │   ├── gate_windows.go
    │   │   ├── go.mod
    │   │   ├── go.sum
    │   │   ├── hash.go
    │   │   ├── host_constants.go
    │   │   ├── host_history.go
    │   │   ├── host_history_test.go
    │   │   ├── host_stream_chunking.go
    │   │   ├── host_stream_chunking_test.go
    │   │   ├── host_stream.go
    │   │   ├── host_stream_test.go
    │   │   ├── lifecycle_probe_windows.go
    │   │   ├── line_diff.go
    │   │   ├── logical_lines.go
    │   │   ├── logical_lines_test.go
    │   │   ├── main.go
    │   │   ├── native_probe.go
    │   │   ├── native_probe_nonwindows.go
    │   │   ├── native_probe_windows.go
    │   │   ├── passthrough_probe_test.go
    │   │   ├── passthrough_probe_windows.go
    │   │   ├── payload_assertions.go
    │   │   ├── payload_assertions_test.go
    │   │   ├── pinned_host.go
    │   │   ├── pinned_host_nonwindows.go
    │   │   ├── pinned_host_windows.go
    │   │   ├── probe.go
    │   │   ├── quirk_probe_windows.go
    │   │   ├── reflow_probe.go
    │   │   ├── reflow_probe_windows.go
    │   │   ├── scrollback.go
    │   │   ├── scrollback_test.go
    │   │   ├── scroll_probe_windows.go
    │   │   ├── seeds.go
    │   │   ├── semantic_probe.go
    │   │   └── semantic_probe_windows.go
    │   ├── f4imgprobe
    │   │   ├── main.go
    │   │   ├── README.txt
    │   │   ├── windowlongptr_32.go
    │   │   └── windowlongptr_64.go
    │   ├── find_hardcoded.go
    │   ├── fishplus_probe.sh
    │   ├── fishplus_testlab
    │   │   ├── fishclient.py
    │   │   ├── TESTLAB.md
    │   │   └── test_patch.py
    │   ├── hardcode
    │   │   ├── hardcode.go
    │   │   └── hardcode_test.go
    │   ├── hardcoded_baseline.txt
    │   ├── icons
    │   │   ├── go.mod
    │   │   ├── go.sum
    │   │   ├── main.go
    │   │   ├── main_test.go
    │   │   └── third_party
    │   │       └── oksvg
    │   │           ├── definitions.go
    │   │           ├── draw.go
    │   │           ├── go.mod
    │   │           ├── icon_cursor.go
    │   │           ├── LICENSE
    │   │           ├── path_cursor.go
    │   │           ├── path_style.go
    │   │           ├── public.go
    │   │           ├── README.md
    │   │           ├── svg_icon.go
    │   │           ├── svg_path.go
    │   │           └── utils.go
    │   ├── langfmt
    │   │   ├── main.go
    │   │   └── main_test.go
    │   ├── releasecheck
    │   │   ├── main.go
    │   │   └── main_test.go
    │   ├── sanitize_native_probe_report.ps1
    │   ├── test_runner.sh
    │   ├── ttytest
    │   │   ├── analyze_log.py
    │   │   ├── README.md
    │   │   ├── scenarios.py
    │   │   └── ttytest.py
    │   ├── verify_native_probe_artifacts.ps1
    │   ├── vtui-screen
    │   │   ├── main.go
    │   │   ├── main_test.go
    │   │   └── README.md
    │   ├── wine_color_probe
    │   │   ├── main.go
    │   │   └── main_other.go
    │   └── wine_syscall_probe
    │       ├── go.mod
    │       ├── main.go
    │       └── probe_amd64.s
    └── vfs
        ├── bulk_copy_test.go
        ├── codepages_cjk.go
        ├── codepages_forced_test.go
        ├── codepages.go
        ├── codepages_iconv_unix.go
        ├── codepages_issue875_test.go
        ├── codepages_nocjk.go
        ├── codepages_test.go
        ├── codepages_unix.go
        ├── codepages_unix_test.go
        ├── codepages_utf8_system_test.go
        ├── codepages_windows.go
        ├── codepages_windows_test.go
        ├── contributions.go
        ├── destination_overwrite_test.go
        ├── device_size_test.go
        ├── disks_unix_gap_test.go
        ├── disks_unix.go
        ├── disks_unix_test.go
        ├── disks_vfs_coverage_test.go
        ├── disks_vfs.go
        ├── disks_vfs_test.go
        ├── disks_windows.go
        ├── disks_windows_test.go
        ├── file_mask.go
        ├── file_mask_test.go
        ├── hidden_rule.go
        ├── hidden_rule_test.go
        ├── hidden_unix.go
        ├── hidden_windows.go
        ├── hostfs
        │   ├── errno_windows.go
        │   ├── hostfs_posix.go
        │   ├── hostfs_posix_test.go
        │   ├── hostfs_windows_coverage_test.go
        │   ├── hostfs_windows.go
        │   ├── hostfs_windows_test.go
        │   └── hostfs_winescape.go
        ├── hostmode
        │   ├── hostmode.go
        │   ├── hostmode_lite.go
        │   └── hostmode_test.go
        ├── hostpath
        │   ├── hostpath_posix.go
        │   └── hostpath_windows.go
        ├── isabs_test.go
        ├── lock_manager_test.go
        ├── metadata.go
        ├── metadata_test.go
        ├── mount_linux.go
        ├── mount_linux_test.go
        ├── mount_other.go
        ├── null_vfs.go
        ├── null_vfs_test.go
        ├── os_vfs_birthtime_bsd_test.go
        ├── os_vfs_birthtime_linux_test.go
        ├── os_vfs_birthtime_windows_test.go
        ├── os_vfs_contract_coverage_test.go
        ├── os_vfs_dot_test.go
        ├── os_vfs_elevation_test.go
        ├── os_vfs.go
        ├── os_vfs_junction_stub.go
        ├── os_vfs_junction_test.go
        ├── os_vfs_listing.go
        ├── os_vfs_listing_other_test.go
        ├── os_vfs_listing_test.go
        ├── os_vfs_listing_windows_test.go
        ├── os_vfs_noreplace_test.go
        ├── os_vfs_open_readonly_test.go
        ├── os_vfs_physical_other.go
        ├── os_vfs_physical_test.go
        ├── os_vfs_physical_unix.go
        ├── os_vfs_physical_windows.go
        ├── os_vfs_platform_darwin.go
        ├── os_vfs_platform_unix.go
        ├── os_vfs_platform_windows.go
        ├── os_vfs_posix_atimespec.go
        ├── os_vfs_posix_atim.go
        ├── os_vfs_reparse_other.go
        ├── os_vfs_reparse_windows.go
        ├── os_vfs_reparse_windows_test.go
        ├── os_vfs_search.go
        ├── os_vfs_search_test.go
        ├── os_vfs_share_windows_test.go
        ├── os_vfs_symlink_test.go
        ├── os_vfs_test.go
        ├── os_vfs_unix_test.go
        ├── os_vfs_windows.go
        ├── os_vfs_windows_test.go
        ├── panel_keys_test.go
        ├── patch_inplace_test.go
        ├── personality.go
        ├── privileges_windows.go
        ├── prompt_hold.go
        ├── prompt_hold_test.go
        ├── pua.go
        ├── pua_test.go
        ├── quick_view.go
        ├── quick_view_test.go
        ├── registry_vfs_windows.go
        ├── registry_vfs_windows_test.go
        ├── rename_noreplace_darwin.go
        ├── rename_noreplace.go
        ├── rename_noreplace_linux.go
        ├── rename_noreplace_linux_test.go
        ├── rename_noreplace_test.go
        ├── rename_noreplace_unix.go
        ├── rename_noreplace_windows.go
        ├── reparse.go
        ├── reparse_test.go
        ├── scanner.go
        ├── scanner_test.go
        ├── session_identity_test.go
        ├── settings.go
        ├── share.go
        ├── share_test.go
        ├── sudo_askpass_test.go
        ├── sudo_askpass_unix.go
        ├── sudo_askpass_windows.go
        ├── sudo_cancel_test.go
        ├── sudo_child_env.go
        ├── sudo_child_env_test.go
        ├── sudo_client.go
        ├── sudo_client_platform_unix.go
        ├── sudo_client_platform_windows.go
        ├── sudo_client_windows_test.go
        ├── sudo_dispatcher_unix.go
        ├── sudo_dispatcher_unix_test.go
        ├── sudo_dispatcher_windows.go
        ├── sudo_elevated.go
        ├── sudo_elevated_test.go
        ├── sudo_frame.go
        ├── sudo_ipc_unix.go
        ├── sudo_ipc_windows.go
        ├── sudo_msg.go
        ├── sudo_notexist_test.go
        ├── sudo_test.go
        ├── sudo_unmount_bsd.go
        ├── sudo_unmount_other.go
        ├── sudo_without_elevation_test.go
        ├── trash_darwin.go
        ├── trash_darwin_test.go
        ├── trash_freedesktop.go
        ├── trash_freedesktop_test.go
        ├── trash.go
        ├── trash_test.go
        ├── trash_windows.go
        ├── trash_windows_posix_test.go
        ├── trash_xdg.go
        ├── trash_xdg_test.go
        ├── unc_smb.go
        ├── unc_smb_test.go
        ├── uri_provider.go
        ├── uri_provider_test.go
        ├── utils.go
        ├── utils_test.go
        ├── vfs.go
        └── vfs_test.go
    
    264 directories, 3220 files
