# Project Structure

    .
    ├── action_copyname_parent_test.go
    ├── action_marked_clipboard_test.go
    ├── action_menu.go
    ├── action_menu_test.go
    ├── action_menu_visibility_test.go
    ├── action_registry.go
    ├── action_registry_test.go
    ├── action_restore_selection_test.go
    ├── actions.go
    ├── actions_test.go
    ├── ai_chat_panel.go
    ├── ai_chat_panel_test.go
    ├── ansi_parser.go
    ├── ansi_parser_test.go
    ├── api.go
    ├── api_test.go
    ├── appearance_settings_test.go
    ├── apply_command_batch.go
    ├── apply_command_batch_test.go
    ├── apply_command.go
    ├── apply_command_output.go
    ├── apply_command_resources.go
    ├── apply_command_resources_test.go
    ├── apply_command_subst.go
    ├── apply_command_subst_test.go
    ├── apply_command_test.go
    ├── apply_command_transcript.go
    ├── apply_shortname_other.go
    ├── apply_shortname_windows.go
    ├── apply_shutdown.go
    ├── archive_index_fallback.go
    ├── archive_index.go
    ├── archive_index_test.go
    ├── arkanoid.go
    ├── arkanoid_test.go
    ├── assets
    │   └── icon
    │       ├── f4-16.svg
    │       ├── f4-24.svg
    │       ├── f4-30.svg
    │       ├── f4-32.svg
    │       ├── f4-36.svg
    │       ├── f4-42.svg
    │       ├── f4.svg
    │       ├── generated
    │       │   ├── f4-1024.png
    │       │   ├── f4-128.png
    │       │   ├── f4-16.png
    │       │   ├── f4-24.png
    │       │   ├── f4-256.png
    │       │   ├── f4-28.png
    │       │   ├── f4-30.png
    │       │   ├── f4-32.png
    │       │   ├── f4-36.png
    │       │   ├── f4-42.png
    │       │   ├── f4-48.png
    │       │   ├── f4-512.png
    │       │   ├── f4-56.png
    │       │   ├── f4-64.png
    │       │   ├── f4.icns
    │       │   └── f4.ico
    │       └── README.md
    ├── async_buffer.go
    ├── async_buffer_test.go
    ├── attributes_dialog.go
    ├── attributes_dialog_unix.go
    ├── attributes_dialog_windows.go
    ├── attributes_test.go
    ├── background_jobs.go
    ├── background_jobs_session_test.go
    ├── background_jobs_test.go
    ├── background_jobs_window.go
    ├── bookmarks_dialog.go
    ├── bookmarks_dialog_test.go
    ├── bookmarks.go
    ├── bookmarks_test.go
    ├── child_env.go
    ├── child_env_test.go
    ├── colorer
    │   └── configs
    │       └── base
    │           └── hrd
    │               └── rgb
    │                   └── fonokai.hrd
    ├── colorer_downloader.go
    ├── colorer_plugin.go
    ├── colorer_plugin_test.go
    ├── colorer_settings.go
    ├── colorer_settings_test.go
    ├── colors.go
    ├── COLORS.md
    ├── colorspace.go
    ├── colorspace_test.go
    ├── colors_test.go
    ├── command_history_paths.go
    ├── command_history_paths_test.go
    ├── command_line.go
    ├── command_line_test.go
    ├── command_prefix_registry.go
    ├── command_prefix_registry_test.go
    ├── command_quoting.go
    ├── command_quoting_test.go
    ├── command_runner.go
    ├── command_runner_test.go
    ├── command_runner_unix.go
    ├── command_runner_unix_test.go
    ├── command_runner_windows.go
    ├── command_runner_windows_test.go
    ├── commands.go
    ├── config.go
    ├── config_test.go
    ├── console_ctrl_handler_other.go
    ├── console_ctrl_handler_windows.go
    ├── cpu_info_darwin.go
    ├── cpu_info.go
    ├── cpu_info_linux.go
    ├── cpu_info_other.go
    ├── cpu_info_windows.go
    ├── debug.log
    ├── delete_trash_test.go
    ├── detach_unix.go
    ├── detach_windows.go
    ├── dialog_layouts_test.go
    ├── dragdrop.go
    ├── DRAGDROP.md
    ├── dragdrop_test.go
    ├── drives_unix.go
    ├── drives_windows.go
    ├── editor_delta_test.go
    ├── editor_features_test.go
    ├── editor_find_all.go
    ├── editor_find_all_test.go
    ├── editor_replace_confirm.go
    ├── editor_restore_keys_test.go
    ├── editor_target_line_test.go
    ├── editor_veto_test.go
    ├── editor_view_ads_test.go
    ├── editor_view.go
    ├── editor_view_test.go
    ├── envman_help_test.go
    ├── external_ui.go
    ├── extui_host.go
    ├── extui_test.go
    ├── far2l_auth.go
    ├── farcolor_exp.go
    ├── farcolor_test.go
    ├── farmenu_file.go
    ├── farmenu_file_test.go
    ├── fast_find_overlay_test.go
    ├── FFI.md
    ├── file_associations_dispatch_test.go
    ├── file_associations_editor.go
    ├── file_associations.go
    ├── file_associations_test.go
    ├── file_associations_ui.go
    ├── filelist_update.sh
    ├── file_op_dialog.go
    ├── file_op_dialog_test.go
    ├── file_ops.go
    ├── file_ops_safety_test.go
    ├── file_ops_test.go
    ├── file_ops_transfer_name_test.go
    ├── file_op_tracker.go
    ├── file_op_tracker_test.go
    ├── file_panel.go
    ├── file_panel_test.go
    ├── file_state.go
    ├── file_state_key_test.go
    ├── file_state_test.go
    ├── find_file.go
    ├── find_file_test.go
    ├── FISH+.md
    ├── FISH_PLUS_S2S.md
    ├── fkeys_hidden_panels_test.go
    ├── folder_history_actions_test.go
    ├── folder_history_navigation_test.go
    ├── fs_info_darwin.go
    ├── fs_info.go
    ├── fs_info_linux.go
    ├── fs_info_other.go
    ├── fs_info_windows.go
    ├── fusefs
    │   ├── bridge.go
    │   ├── bridge_test.go
    │   ├── cli.go
    │   ├── cli_test.go
    │   ├── fusefs.go
    │   ├── FUSE.md
    │   ├── mountspec.go
    │   ├── node_fuse.go
    │   ├── node_unsupported.go
    │   ├── platform_other.go
    │   ├── platform_unix.go
    │   └── registry.go
    ├── FUSE.md
    ├── fuse_mount_action.go
    ├── fuse_mount_list.go
    ├── .github
    │   └── workflows
    │       └── build.yml
    ├── .gitignore
    ├── go.mod
    ├── go.sum
    ├── gpu_info_darwin.go
    ├── gpu_info.go
    ├── gpu_info_linux.go
    ├── gpu_info_other.go
    ├── gpu_info_windows.go
    ├── grabber.go
    ├── grabber_test.go
    ├── gui_font.go
    ├── gui_font_test.go
    ├── gui_unix.go
    ├── gui_windows.go
    ├── hardcoded_strings_test.go
    ├── help
    │   ├── ar.hlf
    │   ├── be.hlf
    │   ├── bn.hlf
    │   ├── cs.hlf
    │   ├── de.hlf
    │   ├── en.hlf
    │   ├── es.hlf
    │   ├── et.hlf
    │   ├── fi.hlf
    │   ├── he.hlf
    │   ├── hi.hlf
    │   ├── hu.hlf
    │   ├── hy.hlf
    │   ├── ja.hlf
    │   ├── ka.hlf
    │   ├── ko.hlf
    │   ├── lt.hlf
    │   ├── lv.hlf
    │   ├── pl.hlf
    │   ├── README.md
    │   ├── ru.hlf
    │   ├── tr.hlf
    │   ├── uk.hlf
    │   └── zh.hlf
    ├── help.go
    ├── help_keys_ar_test.go
    ├── help_keys_he_test.go
    ├── help_keys_ru_test.go
    ├── help_keys_test.go
    ├── help_keys_tr_test.go
    ├── help_lang_test.go
    ├── help_search.go
    ├── help_search_test.go
    ├── help_test.go
    ├── highlight_files.go
    ├── highlight_files_test.go
    ├── HIGHLIGHTING.md
    ├── highlight.ini
    ├── history_dialog.go
    ├── history_dialog_test.go
    ├── history_hint_test.go
    ├── history_provider.go
    ├── history_provider_test.go
    ├── hotkeys.go
    ├── hotkeys_test.go
    ├── hotkeys_ui.go
    ├── hotkeys_ui_test.go
    ├── I18N.md
    ├── IDEAS.md
    ├── image_bmp.go
    ├── image_decode.go
    ├── image_decode_test.go
    ├── image_external.go
    ├── image_external_test.go
    ├── image_formats_test.go
    ├── image_gallery.go
    ├── image_gallery_test.go
    ├── image_native_darwin.go
    ├── image_native_darwin_test.go
    ├── image_pipeline.go
    ├── image_pipeline_test.go
    ├── image_preview.go
    ├── image_preview_test.go
    ├── image_qoi.go
    ├── image_slideshow.go
    ├── image_slideshow_test.go
    ├── IMAGES_PLAN.md
    ├── image_transform.go
    ├── image_transform_test.go
    ├── image_view.go
    ├── image_view_orient_test.go
    ├── image_view_overlay_test.go
    ├── image_view_test.go
    ├── info_panel.go
    ├── info_panel_test.go
    ├── info_usage.go
    ├── ini.go
    ├── ini_test.go
    ├── input_translation.go
    ├── input_translation_test.go
    ├── internal
    │   └── hideconsole
    │       ├── go.mod
    │       └── hideconsole.go
    ├── issue149_test.go
    ├── issue54_test.go
    ├── keybar_injected_test.go
    ├── kitty_graphics.go
    ├── kitty_graphics_test.go
    ├── kitty_metrics_test.go
    ├── kitty_placements.go
    ├── kitty_placements_test.go
    ├── L10N_REPORT_GUIDE.md
    ├── lang
    │   ├── ar.lng
    │   ├── be.lng
    │   ├── bn.lng
    │   ├── coverage_baseline.txt
    │   ├── cs.lng
    │   ├── de.lng
    │   ├── en.lng
    │   ├── es.lng
    │   ├── et.lng
    │   ├── fi.lng
    │   ├── he.lng
    │   ├── hi.lng
    │   ├── hu.lng
    │   ├── hy.lng
    │   ├── ja.lng
    │   ├── ka.lng
    │   ├── ko.lng
    │   ├── lt.lng
    │   ├── lv.lng
    │   ├── pl.lng
    │   ├── README.md
    │   ├── ru.lng
    │   ├── tr.lng
    │   ├── uk.lng
    │   └── zh.lng
    ├── lang_bidi_test.go
    ├── lang_consistency_test.go
    ├── lang_contamination_test.go
    ├── lang.go
    ├── lang_homoglyphs_test.go
    ├── lang_packs.go
    ├── lang_packs_test.go
    ├── lang_scripts_test.go
    ├── lang_test.go
    ├── LICENSE
    ├── LUA.md
    ├── luaplug
    │   ├── convert.go
    │   ├── convert_test.go
    │   ├── f4rpc.go
    │   ├── ffi.go
    │   ├── ffi_test.go
    │   ├── goid.go
    │   ├── luastate_test.go
    │   ├── runtime.go
    │   ├── runtime_test.go
    │   └── sandbox.go
    ├── lua_plugin.go
    ├── lua_plugin_test.go
    ├── macro_ctrlletter_test.go
    ├── macro_export.go
    ├── macro_export_test.go
    ├── macro.go
    ├── macro_host.go
    ├── macro_lua_api.go
    ├── macro_lua.go
    ├── macro_lua_test.go
    ├── macro_plugin_calls.go
    ├── macro_plugin_calls_test.go
    ├── MACROS.md
    ├── macro_test.go
    ├── main.go
    ├── mem_info.go
    ├── mem_info_linux.go
    ├── mem_info_other.go
    ├── mem_info_windows.go
    ├── misc.go
    ├── navigation_mode.go
    ├── navigation_mode_test.go
    ├── packaging
    │   ├── linux
    │   │   └── f4.desktop
    │   └── macos
    │       └── Info.plist
    ├── panel_actions.go
    ├── panel_actions_test.go
    ├── panels_frame.go
    ├── panels_frame_pty_test.go
    ├── panels_frame_test.go
    ├── path_hints.go
    ├── path_hints_test.go
    ├── path_identity.go
    ├── path_identity_test.go
    ├── piecetable
    │   ├── lineindex.go
    │   ├── lineindex_test.go
    │   ├── piecetable.go
    │   └── piecetable_test.go
    ├── plughost_ffi.go
    ├── plughost_ffi_test.go
    ├── plughost.go
    ├── plugin_contributions.go
    ├── plugin_contributions_test.go
    ├── plugin_identity_test.go
    ├── plugin_permissions.go
    ├── plugin_permissions_test.go
    ├── plugin_permissions_ui.go
    ├── plugin_permissions_ui_test.go
    ├── PLUGIN_PLAN.md
    ├── plugins
    │   ├── android
    │   │   ├── adb_integration_test.go
    │   │   ├── adb_sync.go
    │   │   ├── adb_sync_test.go
    │   │   ├── adb_transport.go
    │   │   ├── adb_transport_test.go
    │   │   ├── command_runner_info_test.go
    │   │   ├── device.go
    │   │   ├── device_test.go
    │   │   ├── fish_pool.go
    │   │   ├── fish_pool_test.go
    │   │   ├── info.go
    │   │   ├── info_test.go
    │   │   ├── manager.go
    │   │   ├── manager_test.go
    │   │   ├── pathutil.go
    │   │   ├── pathutil_test.go
    │   │   ├── README.md
    │   │   ├── sync_vfs.go
    │   │   └── sync_vfs_test.go
    │   ├── archive
    │   │   ├── archive.go
    │   │   ├── archive_test.go
    │   │   ├── provider.go
    │   │   ├── provider_test.go
    │   │   ├── repro_test.go
    │   │   ├── vfs.go
    │   │   ├── vfs_nested_test.go
    │   │   ├── vfs_test.go
    │   │   └── zip_encoding.go
    │   ├── chroma
    │   │   ├── chroma.go
    │   │   └── chroma_test.go
    │   ├── dummy_internal
    │   │   ├── dummy_internal.go
    │   │   └── dummy_internal_test.go
    │   ├── dummy_lua
    │   │   ├── plugin.lua
    │   │   └── README.md
    │   ├── dummy_rpc
    │   │   └── main.go
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
    │   │   ├── manager_frame.go
    │   │   ├── manager_ops.go
    │   │   ├── manager_ui.go
    │   │   ├── messages.go
    │   │   ├── model.go
    │   │   ├── model_test.go
    │   │   ├── plugin.go
    │   │   ├── plugin_test.go
    │   │   ├── README.md
    │   │   ├── settings.go
    │   │   ├── settings_test.go
    │   │   ├── strings.go
    │   │   ├── ui_test.go
    │   │   └── vfs_io.go
    │   ├── id3editor
    │   │   ├── plugin.go
    │   │   └── plugin_test.go
    │   ├── ios
    │   │   ├── afc_vfs.go
    │   │   ├── afc_vfs_test.go
    │   │   ├── apps.go
    │   │   ├── core_access.go
    │   │   ├── core_access_stub.go
    │   │   ├── core_access_supported.go
    │   │   ├── core_access_supported_test.go
    │   │   ├── core_tunnel_supported.go
    │   │   ├── core_vfs.go
    │   │   ├── core_vfs_test.go
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
    │   │   ├── selectors.go
    │   │   ├── selectors_test.go
    │   │   └── services.go
    │   ├── netfox
    │   │   ├── crypto.go
    │   │   ├── crypto_test.go
    │   │   ├── dev
    │   │   │   ├── README.md
    │   │   │   └── unxed_f4_issue_316.json
    │   │   ├── dialog.go
    │   │   ├── dialog_test.go
    │   │   ├── fish_clone_session_test.go
    │   │   ├── fish_dialer_test.go
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
    │   │   │   ├── read.go
    │   │   │   ├── read_test.go
    │   │   │   ├── script.go
    │   │   │   ├── script_pwsh_test.go
    │   │   │   ├── script_test.go
    │   │   │   ├── search.go
    │   │   │   ├── search_test.go
    │   │   │   ├── session.go
    │   │   │   ├── session_pwsh_test.go
    │   │   │   ├── session_test.go
    │   │   │   ├── sizes
    │   │   │   ├── WINDOWS_PORT.md
    │   │   │   ├── write.go
    │   │   │   └── write_test.go
    │   │   ├── fish_reconnect_entry_test.go
    │   │   ├── fish_reconnect_test.go
    │   │   ├── fish_vfs.go
    │   │   ├── fish_vfs_test.go
    │   │   ├── ftp_vfs.go
    │   │   ├── lang_test.go
    │   │   ├── netfox.go
    │   │   ├── netfox_test.go
    │   │   ├── registry.go
    │   │   ├── sftp_command_test.go
    │   │   ├── sftp_vfs.go
    │   │   ├── ssh_dial.go
    │   │   ├── ssh_pty.go
    │   │   ├── vfs_abs_test.go
    │   │   └── vfs.go
    │   └── visren
    │       ├── config.go
    │       ├── dialog.go
    │       ├── dialog_test.go
    │       ├── editor.go
    │       ├── editor_test.go
    │       ├── engine_test.go
    │       ├── LICENSE.upstream
    │       ├── masks.go
    │       ├── metadata.go
    │       ├── metadata_test.go
    │       ├── model.go
    │       ├── plugin.go
    │       ├── plugin_test.go
    │       ├── rename.go
    │       ├── rename_test.go
    │       ├── replace.go
    │       └── transforms.go
    ├── plugin_scaffold.go
    ├── plugin_scaffold_test.go
    ├── plugins.go
    ├── PLUGINS.md
    ├── plugring
    │   ├── hello_plugring.lua
    │   └── index.yaml
    ├── plugring.go
    ├── PLUGRING.md
    ├── plugring_meta.go
    ├── plugring_meta_test.go
    ├── plugring_policy_test.go
    ├── plugring_rows_test.go
    ├── plugring_test.go
    ├── plugring_ui.go
    ├── plugring_ui_test.go
    ├── portable_test.go
    ├── process_environment.go
    ├── process_environment_runtime_unix.go
    ├── process_environment_runtime_windows.go
    ├── process_environment_shell.go
    ├── process_environment_test.go
    ├── pty_bsd.go
    ├── pty_darwin.go
    ├── pty_interface.go
    ├── pty_ptm.go
    ├── pty_solaris.go
    ├── pty_test.go
    ├── pty_unix.go
    ├── pty_windows.go
    ├── queue_manager.go
    ├── queue_manager_test.go
    ├── quick_view_panel.go
    ├── quick_view_panel_test.go
    ├── quick_view_provider_api.go
    ├── quick_view_provider_test.go
    ├── README.md
    ├── reconnect.go
    ├── reconnect_test.go
    ├── remote_command.go
    ├── resolve_command_other.go
    ├── resolve_command_windows.go
    ├── REVIEW.md
    ├── rpc_lua_test.go
    ├── rpc_plugin.go
    ├── rpc_plugin_test.go
    ├── rpc_vfs.go
    ├── rpc_vfs_test.go
    ├── rsrc_windows_amd64.syso
    ├── rsrc_windows_arm64.syso
    ├── screenshot.png
    ├── sdk
    │   ├── extui
    │   │   ├── model.go
    │   │   └── model_test.go
    │   ├── f4plugin
    │   │   └── plugin.go
    │   ├── f4rpc
    │   │   ├── mux.go
    │   │   └── mux_test.go
    │   └── lua
    │       └── f4rpc.lua
    ├── semantic.go
    ├── semantic_test.go
    ├── session_unix.go
    ├── session_unix_test.go
    ├── session_windows.go
    ├── share_dialog.go
    ├── share_dialog_test.go
    ├── shell_integration_test.go
    ├── solaris_pty_alloc_test.go
    ├── solaris_pty_backend_test.go
    ├── solaris_pty.go
    ├── solaris_streams.go
    ├── solaris_streams_mock_test.go
    ├── solaris_streams_test.go
    ├── style_combo_colors_test.go
    ├── style_completeness_test.go
    ├── style_default_dark_test.go
    ├── style.go
    ├── style_overrides_test.go
    ├── styles
    │   ├── classic.ini
    │   ├── default_dark.ini
    │   ├── fonokai.ini
    │   ├── fonokai.md
    │   └── modern.ini
    ├── style_test.go
    ├── terminal_log_vfs.go
    ├── terminal_log_vfs_test.go
    ├── TERMINAL.md
    ├── terminal_selection_test.go
    ├── terminal_view.go
    ├── terminal_view_test.go
    ├── TERMINAL_WINDOWS.md
    ├── test_cache_helper_test.go
    ├── test_fallback_lang_test.go
    ├── test_main_test.go
    ├── TEST_OPTIMIZATION_PLAN.md
    ├── test_plugins.sh
    ├── test_resurrect.sh
    ├── text_editor_bridge.go
    ├── text_editor_bridge_test.go
    ├── textlayout
    │   ├── wrap.go
    │   └── wrap_test.go
    ├── themed_table.go
    ├── title.go
    ├── title_test.go
    ├── title_unix.go
    ├── title_windows.go
    ├── tools
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
    │   │           ├── .gitignore
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
    │   └── test_runner.sh
    ├── top_bar.go
    ├── top_bar_test.go
    ├── translate_kitty.go
    ├── translate_kitty_test.go
    ├── updater.go
    ├── updater_repro_test.go
    ├── updater_test.go
    ├── uri_navigation_test.go
    ├── user_menu.go
    ├── user_menu_ini.go
    ├── user_menu_ini_test.go
    ├── user_menu_subst.go
    ├── user_menu_subst_test.go
    ├── user_menu_ui.go
    ├── user_menu_ui_test.go
    ├── UX_GUIDELINES.md
    ├── vfs
    │   ├── bulk_copy_test.go
    │   ├── codepages.go
    │   ├── codepages_test.go
    │   ├── codepages_unix.go
    │   ├── codepages_unix_test.go
    │   ├── codepages_windows.go
    │   ├── contributions.go
    │   ├── destination_overwrite_test.go
    │   ├── hidden_unix.go
    │   ├── hidden_windows.go
    │   ├── isabs_test.go
    │   ├── lock_manager_test.go
    │   ├── null_vfs.go
    │   ├── null_vfs_test.go
    │   ├── os_vfs_dot_test.go
    │   ├── os_vfs.go
    │   ├── os_vfs_junction_stub.go
    │   ├── os_vfs_noreplace_test.go
    │   ├── os_vfs_physical_other.go
    │   ├── os_vfs_physical_test.go
    │   ├── os_vfs_physical_unix.go
    │   ├── os_vfs_physical_windows.go
    │   ├── os_vfs_platform_unix.go
    │   ├── os_vfs_platform_windows.go
    │   ├── os_vfs_posix_atimespec.go
    │   ├── os_vfs_posix_atim.go
    │   ├── os_vfs_symlink_test.go
    │   ├── os_vfs_test.go
    │   ├── os_vfs_unix_test.go
    │   ├── os_vfs_windows.go
    │   ├── os_vfs_windows_test.go
    │   ├── privileges_windows.go
    │   ├── quick_view.go
    │   ├── quick_view_test.go
    │   ├── rename_noreplace_darwin.go
    │   ├── rename_noreplace.go
    │   ├── rename_noreplace_linux.go
    │   ├── rename_noreplace_unix.go
    │   ├── rename_noreplace_windows.go
    │   ├── scanner.go
    │   ├── scanner_test.go
    │   ├── session_identity_test.go
    │   ├── share.go
    │   ├── share_test.go
    │   ├── sudo_askpass_unix.go
    │   ├── sudo_askpass_windows.go
    │   ├── sudo_client.go
    │   ├── sudo_dispatcher_unix.go
    │   ├── sudo_dispatcher_windows.go
    │   ├── sudo_ipc_unix.go
    │   ├── sudo_ipc_windows.go
    │   ├── sudo_msg.go
    │   ├── sudo_test.go
    │   ├── trash_darwin.go
    │   ├── trash_freedesktop.go
    │   ├── trash_freedesktop_test.go
    │   ├── trash.go
    │   ├── trash_test.go
    │   ├── trash_windows.go
    │   ├── uri_provider.go
    │   ├── uri_provider_test.go
    │   ├── utils.go
    │   ├── utils_test.go
    │   └── vfs.go
    ├── VFS.md
    ├── viewer_backend.go
    ├── viewer_backend_test.go
    ├── viewer_editor_history.go
    ├── viewer_editor_history_test.go
    ├── viewer_view.go
    ├── viewer_view_test.go
    ├── visren_editor_bridge.go
    ├── VTML.md
    ├── vtvibe
    │   ├── memtree.go
    │   ├── pack.go
    │   ├── provider.go
    │   ├── provider_test.go
    │   ├── session.go
    │   ├── session_test.go
    │   └── vfs.go
    ├── vtvibe_host.go
    ├── vtvibe_host_test.go
    ├── vtvibe.md
    ├── wasm_plugin.go
    ├── wasm_plugin_test.go
    ├── window_icon_windows.go
    ├── window_icon_windows_test.go
    ├── word_nav.go
    ├── word_nav_test.go
    ├── workspace_routing_test.go
    ├── workspace_session.go
    └── workspace_session_test.go
    
    54 directories, 798 files
