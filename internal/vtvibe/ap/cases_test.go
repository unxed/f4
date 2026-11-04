package ap

// This file is a line-by-line port of implementation/cases.py from
// github.com/unxed/ap: the same declarative cases, translated into Go
// struct literals, run through the same assertions as
// implementation/run_tests.py's run_case(). Keep it in sync with the
// upstream file's case *names* and *content* (not necessarily its Go
// formatting) so a diff against the Python source stays meaningful; do not
// "improve" a case's patch/files/expectations here without also filing the
// same improvement upstream, since the point of this file is that the two
// implementations are checked against one identical set of examples.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type apCase struct {
	name            string
	files           map[string]string
	patch           string
	strict          bool
	expectStatus    Status
	expectError     ErrCode
	expectFiles     map[string]string
	expectUnchanged []string
	expectMissing   []string
	expectStdout    []string
	expectMD        []string
}

var apCases = []apCase{
	{
		name:        "c04_markdown_fences_do_not_leak_into_the_source",
		files:       map[string]string{"a.txt": "line1\nline2\nline3\n"},
		patch:       "```\naa000004 AP 3.2\n\naa000004 FILE\na.txt\n\naa000004 REPLACE\naa000004 snippet\nline2\naa000004 content\nLINE2\n```\n",
		expectFiles: map[string]string{"a.txt": "line1\nLINE2\nline3\n"},
	},
	{
		name:  "c05_id_drift_keeps_both_ids_working",
		files: map[string]string{"a.txt": "line1\nline2\nline3\n"},
		patch: "aa000005 AP 3.2\n\naa000005 FILE\na.txt\n\n" +
			"aa000005 REPLACE\naa000005 snippet\nline1\naa000005 content\nLINE1\n\n" +
			"bb000005 REPLACE\nbb000005 snippet\nline2\nbb000005 content\nLINE2\n\n" +
			"aa000005 REPLACE\naa000005 snippet\nline3\naa000005 content\nLINE3\n",
		expectFiles: map[string]string{"a.txt": "LINE1\nLINE2\nLINE3\n"},
	},
	{
		name:        "c06_unknown_header_version_is_still_parsed",
		files:       map[string]string{"a.txt": "line1\nline2\n"},
		patch:       "aa000006 AP 9.9\n\naa000006 FILE\na.txt\n\naa000006 REPLACE\naa000006 snippet\nline2\naa000006 content\nLINE2\n",
		expectFiles: map[string]string{"a.txt": "line1\nLINE2\n"},
	},
	{
		name:        "c07_directive_case_and_colons_are_forgiven",
		files:       map[string]string{"a.txt": "line1\nline2\n"},
		patch:       "aa000007 AP 3.2\n\naa000007 file:\na.txt\n\naa000007 replace\naa000007 SNIPPET:\nline2\naa000007 Content:\nLINE2\n",
		expectFiles: map[string]string{"a.txt": "line1\nLINE2\n"},
	},
	{
		name:  "c08_missing_action_directive_splits_instead_of_losing_a_change",
		files: map[string]string{"a.txt": "one\ntwo\nthree\n"},
		patch: "aa000008 AP 3.2\n\naa000008 FILE\na.txt\n\n" +
			"aa000008 REPLACE\naa000008 snippet\none\naa000008 content\nONE\n\n" +
			"aa000008 snippet\nthree\naa000008 content\nTHREE\n",
		expectFiles: map[string]string{"a.txt": "ONE\ntwo\nTHREE\n"},
	},
	{
		name:        "c09_missing_snippet_keyword_is_recovered",
		files:       map[string]string{"a.txt": "one\ntwo\n"},
		patch:       "aa000009 AP 3.2\n\naa000009 FILE\na.txt\n\naa000009 REPLACE\ntwo\naa000009 content\nTWO\n",
		expectFiles: map[string]string{"a.txt": "one\nTWO\n"},
	},
	{
		name:        "c10_inline_file_path",
		files:       map[string]string{"a.txt": "one\ntwo\n"},
		patch:       "aa000010 AP 3.2\n\naa000010 FILE a.txt\n\naa000010 REPLACE\naa000010 snippet\ntwo\naa000010 content\nTWO\n",
		expectFiles: map[string]string{"a.txt": "one\nTWO\n"},
	},
	{
		name:        "c11_inline_file_path_with_newline_mode",
		files:       map[string]string{"a.txt": "one\ntwo\n"},
		patch:       "aa000011 AP 3.2\n\naa000011 FILE CRLF a.txt\n\naa000011 REPLACE\naa000011 snippet\ntwo\naa000011 content\nTWO\n",
		expectFiles: map[string]string{"a.txt": "one\r\nTWO\r\n"},
	},
	{
		name:            "c12_empty_file_path_is_a_clear_error",
		files:           map[string]string{"a.txt": "one\n"},
		patch:           "aa000012 AP 3.2\n\naa000012 FILE\n\naa000012 REPLACE\naa000012 snippet\none\naa000012 content\nONE\n",
		expectStatus:    StatusFailed,
		expectError:     ErrInvalidPatchFile,
		expectUnchanged: []string{"a.txt"},
	},
	{
		name:  "c13_duplicate_content_is_rejected",
		files: map[string]string{"a.txt": "one\n"},
		patch: "aa000013 AP 3.2\n\naa000013 FILE\na.txt\n\naa000013 REPLACE\n" +
			"aa000013 snippet\none\naa000013 content\nONE\naa000013 content\nTWO\n",
		expectStatus:    StatusFailed,
		expectError:     ErrInvalidPatchFile,
		expectUnchanged: []string{"a.txt"},
	},
	{
		name:   "c21_strict_rejects_the_missing_action_directive",
		files:  map[string]string{"a.txt": "one\ntwo\nthree\n"},
		strict: true,
		patch: "aa000021 AP 3.2\n\naa000021 FILE\na.txt\n\n" +
			"aa000021 REPLACE\naa000021 snippet\none\naa000021 content\nONE\n\n" +
			"aa000021 snippet\nthree\naa000021 content\nTHREE\n",
		expectStatus:    StatusFailed,
		expectError:     ErrInvalidPatchFile,
		expectUnchanged: []string{"a.txt"},
	},
	{
		name:            "c22_strict_rejects_an_unsupported_version",
		files:           map[string]string{"a.txt": "one\n"},
		strict:          true,
		patch:           "aa000022 AP 9.9\n\naa000022 FILE\na.txt\n\naa000022 REPLACE\naa000022 snippet\none\naa000022 content\nONE\n",
		expectStatus:    StatusFailed,
		expectError:     ErrInvalidPatchFile,
		expectUnchanged: []string{"a.txt"},
	},
	{
		name:            "c01_ambiguous_snippet_refuses_to_guess",
		files:           map[string]string{"a.py": "def f():\n    a = 1\n    return a\n\ndef g():\n    a = 1\n    return a\n"},
		patch:           "aa000001 AP 3.2\n\naa000001 FILE\na.py\n\naa000001 REPLACE\naa000001 snippet\n    a = 1\naa000001 content\n    a = 42\n",
		expectStatus:    StatusPartial,
		expectUnchanged: []string{"a.py"},
		expectMD:        []string{"AMBIGUOUS_MATCH", "anchor", "Current content of the file"},
	},
	{
		name:        "c02_anchor_resolves_the_ambiguity",
		files:       map[string]string{"a.py": "def f():\n    a = 1\n    return a\n\ndef g():\n    a = 1\n    return a\n"},
		patch:       "aa000002 AP 3.2\n\naa000002 FILE\na.py\n\naa000002 REPLACE\naa000002 anchor\ndef g():\naa000002 snippet\n    a = 1\naa000002 content\n    a = 42\n",
		expectFiles: map[string]string{"a.py": "def f():\n    a = 1\n    return a\n\ndef g():\n    a = 42\n    return a\n"},
	},
	{
		name:  "c03_repeated_locator_is_sequential_not_ambiguous",
		files: map[string]string{"a.txt": "x\nx\nx\n"},
		patch: "aa000003 AP 3.2\n\naa000003 FILE\na.txt\n\n" +
			"aa000003 REPLACE\naa000003 snippet\nx\naa000003 content\none\n\n" +
			"aa000003 REPLACE\naa000003 snippet\nx\naa000003 content\ntwo\n",
		expectFiles: map[string]string{"a.txt": "one\ntwo\nx\n"},
	},

	// ------------------------------------------------------------ chat noise
	{
		name:        "c14_dropped_indentation_is_restored",
		files:       map[string]string{"a.py": "def f():\n    if x:\n        do_a()\n        do_b()\n"},
		patch:       "aa000014 AP 3.2\n\naa000014 FILE\na.py\n\naa000014 INSERT_AFTER\naa000014 snippet\ndo_a()\naa000014 content\ndo_c()\n",
		expectFiles: map[string]string{"a.py": "def f():\n    if x:\n        do_a()\n        do_c()\n        do_b()\n"},
	},
	{
		name:        "c15_explicit_indentation_is_never_touched",
		files:       map[string]string{"a.py": "def f():\n    if x:\n        do_a()\n        do_b()\n"},
		patch:       "aa000015 AP 3.2\n\naa000015 FILE\na.py\n\naa000015 INSERT_AFTER\naa000015 snippet\ndo_a()\naa000015 content\n        do_c()\n",
		expectFiles: map[string]string{"a.py": "def f():\n    if x:\n        do_a()\n        do_c()\n        do_b()\n"},
	},
	{
		name:            "c16_partial_line_snippet_gets_an_actionable_message",
		files:           map[string]string{"a.py": "def f():\n    x = compute(1, 2) + 1\n"},
		patch:           "aa000016 AP 3.2\n\naa000016 FILE\na.py\n\naa000016 REPLACE\naa000016 snippet\ncompute(1, 2)\naa000016 content\n    x = compute(3, 4) + 1\n",
		expectStatus:    StatusPartial,
		expectUnchanged: []string{"a.py"},
		expectMD:        []string{"fragment of an existing line", "SNIPPET_NOT_FOUND"},
	},
	{
		name:  "c17_reversed_range_is_auto_corrected",
		files: map[string]string{"a.txt": "keep\nstart\nmiddle\nend\nkeep2\n"},
		patch: "aa000017 AP 3.2\n\naa000017 FILE\na.txt\n\naa000017 REPLACE\n" +
			"aa000017 snippet\nend\naa000017 snippet_tail\nstart\naa000017 content\nNEW\n",
		expectFiles: map[string]string{"a.txt": "keep\nNEW\nkeep2\n"},
	},
	{
		name:  "c18_point_action_with_a_range_inserts_at_the_range_edge",
		files: map[string]string{"a.txt": "start\nmiddle\nend\ntail\n"},
		patch: "aa000018 AP 3.2\n\naa000018 FILE\na.txt\n\naa000018 INSERT_AFTER\n" +
			"aa000018 snippet\nstart\naa000018 snippet_tail\nend\naa000018 content\nNEW\n",
		expectFiles: map[string]string{"a.txt": "start\nmiddle\nend\nNEW\ntail\n"},
	},

	// ----------------------------------------------------------- output shape
	{
		name:            "c20_untouched_files_are_not_rewritten",
		files:           map[string]string{"a.txt": "one\ntwo"},
		patch:           "aa000020 AP 3.2\n\naa000020 FILE\na.txt\n\naa000020 REPLACE\naa000020 snippet\nnope\naa000020 content\nNOPE\n",
		expectStatus:    StatusPartial,
		expectUnchanged: []string{"a.txt"},
	},

	// ------------------------------------------------------------ strict mode
	{
		name:   "c23_strict_is_atomic_across_files",
		files:  map[string]string{"a.txt": "one\n", "b.txt": "two\n"},
		strict: true,
		patch: "aa000023 AP 3.2\n\naa000023 FILE\na.txt\n\naa000023 REPLACE\n" +
			"aa000023 snippet\none\naa000023 content\nONE\n\n" +
			"aa000023 FILE\nb.txt\n\naa000023 REPLACE\naa000023 snippet\nmissing\n" +
			"aa000023 content\nX\n",
		expectStatus:    StatusFailed,
		expectError:     ErrSnippetNotFound,
		expectUnchanged: []string{"a.txt", "b.txt"},
	},

	// ------------------------------------------------------------ the report
	{
		name:  "c24_report_shows_what_was_already_applied",
		files: map[string]string{"a.txt": "one\ntwo\n"},
		patch: "aa000024 AP 3.2\n\naa000024 FILE\na.txt\n\n" +
			"aa000024 REPLACE\naa000024 snippet\none\naa000024 content\nONE\n\n" +
			"aa000024 REPLACE\naa000024 snippet\nmissing line\naa000024 content\nX\n",
		expectStatus: StatusPartial,
		expectFiles:  map[string]string{"a.txt": "ONE\ntwo\n"},
		expectMD:     []string{"already changed in this file", "-one", "+ONE", "The patch that failed"},
	},
	{
		name:          "c25_clean_rerun_removes_the_stale_report",
		files:         map[string]string{"a.txt": "one\n", "afailed.md": "# stale report\n"},
		patch:         "aa000025 AP 3.2\n\naa000025 FILE\na.txt\n\naa000025 REPLACE\naa000025 snippet\none\naa000025 content\nONE\n",
		expectFiles:   map[string]string{"a.txt": "ONE\n"},
		expectMissing: []string{"afailed.md"},
	},
	{
		name:  "c26_an_ap_example_inside_content_is_not_mistaken_for_drift",
		files: map[string]string{"doc.md": "old\n"},
		patch: "aa000026 AP 3.2\n\naa000026 FILE\ndoc.md\n\naa000026 RECREATE\naa000026 content\n" +
			"Example:\n\n```\ne4a2f1b8 FILE\nx.py\n\ne4a2f1b8 REPLACE\ne4a2f1b8 snippet\n" +
			"old\ne4a2f1b8 content\nnew\n```\n\nEnd.",
		expectFiles: map[string]string{"doc.md": "Example:\n\n```\ne4a2f1b8 FILE\nx.py\n\ne4a2f1b8 REPLACE\n" +
			"e4a2f1b8 snippet\nold\ne4a2f1b8 content\nnew\n```\n\nEnd.\n"},
	},
	{
		name:        "c27_cli_entry_point_is_importable_and_consistent",
		files:       map[string]string{"a.txt": "one\n"},
		patch:       "aa000027 AP 3.2\n\naa000027 FILE\na.txt\n\naa000027 REPLACE\naa000027 snippet\none\naa000027 content\nONE\n",
		expectFiles: map[string]string{"a.txt": "ONE\n"},
	},
	{
		name:  "c28_redundant_project_prefix_creation",
		files: map[string]string{"src/a.txt": "old content\n"},
		patch: "bb000028 AP 3.2\n\n" +
			"bb000028 FILE\n" +
			"my_project/src/new_file.txt\n\n" +
			"bb000028 CREATE\n" +
			"bb000028 content\n" +
			"New file created successfully!\n",
		// The target file is brand new and the patch does not declare a
		// newline mode, so per §2.4 the patcher falls back to the OS
		// default line ending, not "\n" unconditionally - matching the
		// reference's use of os.linesep here (this is the one case in the
		// suite where that choice is actually observable, since every
		// other case either edits an existing file or gives an explicit
		// FILE newline argument).
		expectFiles: map[string]string{
			"src/new_file.txt": "New file created successfully!" + osLineSep(),
		},
	},
	{
		name:  "c29_redundant_project_prefix_existing_file",
		files: map[string]string{"src/a.txt": "old content\n"},
		patch: "cc000029 AP 3.2\n\n" +
			"cc000029 FILE\n" +
			"my_project/src/a.txt\n\n" +
			"cc000029 REPLACE\n" +
			"cc000029 snippet\n" +
			"old content\n" +
			"cc000029 content\n" +
			"new content\n",
		expectFiles: map[string]string{
			"src/a.txt": "new content\n",
		},
	},
	{
		name:  "c30_locator_never_matches_text_the_patch_itself_wrote",
		files: map[string]string{"m.py": "def a():\n    log(\"start\")\n    return 1\n\ndef b():\n    return 2\n"},
		patch: "aa000030 AP 3.2\n\naa000030 FILE\nm.py\n\n" +
			"aa000030 INSERT_AFTER\naa000030 snippet\ndef b():\n" +
			"aa000030 content\n    log(\"start\")\n\n" +
			"aa000030 REPLACE\naa000030 snippet\nlog(\"start\")\n" +
			"aa000030 content\n    log(\"BEGIN\")\n",
		expectFiles: map[string]string{"m.py": "def a():\n    log(\"BEGIN\")\n    return 1\n\ndef b():\n    log(\"start\")\n    return 2\n"},
	},
	{
		name:  "c31_idempotency_probe_does_not_match_its_own_output",
		files: map[string]string{"m.py": "def a(x):\n    old_a()\n\ndef b(x):\n    old_b()\n"},
		patch: "aa000031 AP 3.2\n\naa000031 FILE\nm.py\n\n" +
			"aa000031 REPLACE\naa000031 snippet\nold_a()\naa000031 content\ncheck(x)\n\n" +
			"aa000031 REPLACE\naa000031 snippet\nold_bb()\naa000031 content\ncheck(x)\n",
		expectStatus: StatusPartial,
		expectMD:     []string{"Snippet not found"},
		expectFiles:  map[string]string{"m.py": "def a(x):\n    check(x)\n\ndef b(x):\n    old_b()\n"},
	},
	{
		name:            "c32_single_line_locator_does_not_eat_a_line_prefix",
		files:           map[string]string{"m.py": "class C:\n    def __init__(self):\n        self.count = 0\n"},
		patch:           "aa000032 AP 3.2\n\naa000032 FILE\nm.py\n\naa000032 REPLACE\naa000032 snippet\ncount = 0\naa000032 content\ncount = 5\n",
		expectStatus:    StatusPartial,
		expectUnchanged: []string{"m.py"},
	},
	{
		name:        "c33_single_line_locator_still_ignores_list_numbering",
		files:       map[string]string{"m.py": "# 1. First item\n# 2. Second item\n"},
		patch:       "aa000033 AP 3.2\n\naa000033 FILE\nm.py\n\naa000033 REPLACE\naa000033 snippet\nFirst item\naa000033 content\n# First item (replaced)\n",
		expectFiles: map[string]string{"m.py": "# First item (replaced)\n# 2. Second item\n"},
	},
	{
		name:  "c34_empty_content_followed_by_a_directive_is_a_delete",
		files: map[string]string{"a.txt": "a\nb\nc\n"},
		patch: "aa000034 AP 3.2\n\naa000034 FILE\na.txt\n\n" +
			"aa000034 REPLACE\naa000034 snippet\nb\naa000034 content\n\n" +
			"aa000034 REPLACE\naa000034 snippet\nc\naa000034 content\nC\n",
		expectFiles: map[string]string{"a.txt": "a\nC\n"},
	},
	{
		name:  "c35_empty_content_at_end_of_patch_is_treated_as_truncation",
		files: map[string]string{"a.txt": "a\nb\nc\n"},
		patch: "aa000035 AP 3.2\n\naa000035 FILE\na.txt\n\n" +
			"aa000035 REPLACE\naa000035 snippet\nb\naa000035 content\n",
		expectStatus:    StatusPartial,
		expectUnchanged: []string{"a.txt"},
		expectMD:        []string{"PATCH_TRUNCATED"},
	},
	{
		name:  "c36_end_directive_proves_a_trailing_empty_content_is_deliberate",
		files: map[string]string{"a.txt": "a\nb\nc\n"},
		patch: "aa000036 AP 3.2\n\naa000036 FILE\na.txt\n\n" +
			"aa000036 REPLACE\naa000036 snippet\nb\naa000036 content\n\naa000036 END\n",
		expectFiles: map[string]string{"a.txt": "a\nc\n"},
	},
	{
		name:  "c37_top_level_block_is_not_inserted_inside_a_function",
		files: map[string]string{"main.go": "package main\n\nfunc A(x int) int {\n\treturn 0\n}\n\nfunc C() {}\n"},
		patch: "aa000037 AP 3.2\n\naa000037 FILE\nmain.go\n\n" +
			"aa000037 INSERT_AFTER\naa000037 snippet\nfunc A(x int) int {\n" +
			"aa000037 content\nfunc B(x int) int {\n\treturn x + 1\n}\n",
		expectFiles: map[string]string{"main.go": "package main\n\nfunc A(x int) int {\n\treturn 0\n}\n\nfunc B(x int) int {\n\treturn x + 1\n}\n\nfunc C() {}\n"},
	},
	{
		name:  "c38_nested_block_is_left_where_the_model_put_it",
		files: map[string]string{"main.go": "package main\n\nfunc A() {\n\tx := 1\n\t_ = x\n}\n"},
		patch: "aa000038 AP 3.2\n\naa000038 FILE\nmain.go\n\n" +
			"aa000038 INSERT_AFTER\naa000038 snippet\nx := 1\n" +
			"aa000038 content\n\tgo func() {\n\t\twork()\n\t}()\n",
		expectFiles: map[string]string{"main.go": "package main\n\nfunc A() {\n\tx := 1\n\tgo func() {\n\t\twork()\n\t}()\n\t_ = x\n}\n"},
	},
	{
		name:  "c39_scope_end_replaces_a_whole_brace_block",
		files: map[string]string{"main.go": "package main\n\nfunc A(x int) int {\n\tif x > 0 {\n\t\treturn x\n\t}\n\treturn 0\n}\n\nfunc C() {}\n"},
		patch: "aa000039 AP 3.2\n\naa000039 FILE\nmain.go\n\n" +
			"aa000039 REPLACE\naa000039 snippet\nfunc A(x int) int {\n" +
			"aa000039 scope_end 1\n" +
			"aa000039 content\nfunc A(x int) int {\n\treturn x\n}\n",
		expectFiles: map[string]string{"main.go": "package main\n\nfunc A(x int) int {\n\treturn x\n}\n\nfunc C() {}\n"},
	},
	{
		name:  "c40_scope_end_deletes_a_whole_indented_block",
		files: map[string]string{"m.py": "def a(x):\n    if x:\n        return 1\n    return 0\n\ndef b(x):\n    return 2\n"},
		patch: "aa000040 AP 3.2\n\naa000040 FILE\nm.py\n\n" +
			"aa000040 DELETE\naa000040 snippet\ndef a(x):\n" +
			"aa000040 scope_end 1\naa000040 include_trailing_blank_lines 1\n",
		expectFiles: map[string]string{"m.py": "def b(x):\n    return 2\n"},
	},
	{
		name:  "c41_locator_removed_by_an_earlier_modification_is_named_as_such",
		files: map[string]string{"m.py": "def a(x):\n    log(\"hi\")\n    return x\n\ndef b(x):\n    return 2\n"},
		patch: "aa000041 AP 3.2\n\naa000041 FILE\nm.py\n\n" +
			"aa000041 REPLACE\naa000041 snippet\ndef a(x):\naa000041 scope_end 1\n" +
			"aa000041 content\ndef a(x):\n    return x * 2\n\n" +
			"aa000041 REPLACE\naa000041 snippet\nlog(\"hi\")\n" +
			"aa000041 content\n    log(\"bye\")\n",
		expectStatus: StatusPartial,
		expectMD:     []string{"LOCATOR_CONSUMED"},
		expectFiles:  map[string]string{"m.py": "def a(x):\n    return x * 2\ndef b(x):\n    return 2\n"},
	},
	{
		name:  "c42_anchor_duplicated_by_the_patch_itself_stays_unambiguous",
		files: map[string]string{"m.py": "def a():\n    setup()\n    work()\n\ndef b():\n    work()\n"},
		patch: "aa000042 AP 3.2\n\naa000042 FILE\nm.py\n\n" +
			"aa000042 INSERT_BEFORE\naa000042 snippet\ndef b():\n" +
			"aa000042 content\ndef c():\n    setup()\n\n" +
			"aa000042 REPLACE\naa000042 anchor\ndef b():\n" +
			"aa000042 snippet\nwork()\naa000042 content\n    work2()\n",
		expectFiles: map[string]string{"m.py": "def a():\n    setup()\n    work()\n\ndef c():\n    setup()\ndef b():\n    work2()\n"},
	},
	{
		name:  "c43_unbalanced_result_is_reported",
		files: map[string]string{"main.go": "package main\n\nfunc A() {\n\tif true {\n\t\twork()\n\t}\n}\n"},
		patch: "aa000043 AP 3.2\n\naa000043 FILE\nmain.go\n\n" +
			"aa000043 REPLACE\naa000043 snippet\nif true {\n" +
			"aa000043 snippet_tail\nwork()\n" +
			"aa000043 content\n\twork2()\n",
		expectStdout: []string{"off by -1"},
	},
	{
		name:  "c44_rewritten_file_gains_a_trailing_newline",
		files: map[string]string{"f.txt": "a\nb"},
		patch: "aa000044 AP 3.2\n\naa000044 FILE\nf.txt\n\n" +
			"aa000044 REPLACE\naa000044 snippet\nb\naa000044 content\nB\n",
		expectFiles: map[string]string{"f.txt": "a\nB\n"},
	},
	{
		name:  "c45_untouched_file_is_not_given_a_trailing_newline",
		files: map[string]string{"f.txt": "a\nb", "g.txt": "x\n"},
		patch: "aa000045 AP 3.2\n\naa000045 FILE\ng.txt\n\n" +
			"aa000045 REPLACE\naa000045 snippet\nx\naa000045 content\nX\n",
		expectFiles:     map[string]string{"g.txt": "X\n"},
		expectUnchanged: []string{"f.txt"},
	},
	{
		name:  "c46_trailing_newline_uses_the_files_own_line_ending",
		files: map[string]string{"f.txt": "a\r\nb"},
		patch: "aa000046 AP 3.2\n\naa000046 FILE\nf.txt\n\n" +
			"aa000046 REPLACE\naa000046 snippet\nb\naa000046 content\nB\n",
		expectFiles: map[string]string{"f.txt": "a\r\nB\r\n"},
	},
	{
		// Upstream bug: `found` was unbound when creating a new file inside an
		// EXISTING directory (Python UnboundLocalError). resolvePathPrefix never
		// had it; kept in sync with upstream's case list.
		name:  "c47_create_file_in_existing_dir_does_not_crash",
		files: map[string]string{"archive/old.go": "package archive\n"},
		patch: "aa000047 AP 3.2\n\naa000047 FILE LF\narchive/new_test.go\n\n" +
			"aa000047 CREATE\naa000047 content\npackage archive\n",
		expectFiles: map[string]string{"archive/new_test.go": "package archive\n",
			"archive/old.go": "package archive\n"},
	},
	{
		// A REPLACE that only dedents was taken for "already applied"
		// (normalizeBlock ignores indentation) or re-indented back by
		// reindentContent, so the file silently stayed unchanged.
		name:  "c48_dedent_only_replace_is_applied",
		files: map[string]string{"c.yml": "a:\n  b: 1\n  c: 2\n"},
		patch: "aa000048 AP 3.2\n\naa000048 FILE\nc.yml\n\n" +
			"aa000048 REPLACE\naa000048 snippet\n  c: 2\naa000048 content\nc: 2\n",
		expectFiles: map[string]string{"c.yml": "a:\n  b: 1\nc: 2\n"},
	},
	{
		name:  "c49_multiline_dedent_only_replace_is_applied",
		files: map[string]string{"m.py": "class C:\n    def m(self):\n        return 1\n"},
		patch: "aa000049 AP 3.2\n\naa000049 FILE\nm.py\n\n" +
			"aa000049 REPLACE\naa000049 snippet\n    def m(self):\n        return 1\n" +
			"aa000049 content\ndef m(self):\n    return 1\n",
		expectFiles: map[string]string{"m.py": "class C:\ndef m(self):\n    return 1\n"},
	},
	{
		name:  "c50_indent_only_replace_is_applied",
		files: map[string]string{"m.py": "def f():\n    if x:\n        a()\n    b()\n"},
		patch: "aa000050 AP 3.2\n\naa000050 FILE\nm.py\n\n" +
			"aa000050 REPLACE\naa000050 snippet\n    b()\n" +
			"aa000050 content\n        b()\n",
		expectFiles: map[string]string{"m.py": "def f():\n    if x:\n        a()\n        b()\n"},
	},
	{
		// Re-running the dedent: the region already has the requested indentation.
		name:  "c51_dedent_only_replace_already_applied_is_skipped",
		files: map[string]string{"c.yml": "a:\n  b: 1\nc: 2\n"},
		patch: "aa000051 AP 3.2\n\naa000051 FILE\nc.yml\n\n" +
			"aa000051 REPLACE\naa000051 snippet\n  c: 2\naa000051 content\nc: 2\n",
		expectUnchanged: []string{"c.yml"},
		expectStdout:    []string{"already applied"},
	},
	{
		name:  "c52_indent_only_replace_already_applied_is_skipped",
		files: map[string]string{"m.py": "def f():\n    if x:\n        a()\n        b()\n"},
		patch: "aa000052 AP 3.2\n\naa000052 FILE\nm.py\n\n" +
			"aa000052 REPLACE\naa000052 snippet\n    b()\n" +
			"aa000052 content\n        b()\n",
		expectUnchanged: []string{"m.py"},
		expectStdout:    []string{"already applied"},
	},
	{
		// The model dropped the base indentation in BOTH snippet and content;
		// once re-indented to the target, the content is already there.
		name:  "c53_reindented_replace_already_applied_is_skipped",
		files: map[string]string{"m.py": "def f():\n    if x:\n        y()\n"},
		patch: "aa000053 AP 3.2\n\naa000053 FILE\nm.py\n\n" +
			"aa000053 REPLACE\naa000053 snippet\nif x:\ny()\n" +
			"aa000053 content\nif x:\n    y()\n",
		expectUnchanged: []string{"m.py"},
		expectStdout:    []string{"already applied"},
	},
	{
		// A REPLACE whose snippet and content are identical changes nothing,
		// even when both are written at an indentation the file does not use.
		name:  "c54_noop_replace_with_off_indentation_is_skipped",
		files: map[string]string{"m.py": "def f():\n    x = 1\n"},
		patch: "aa000054 AP 3.2\n\naa000054 FILE\nm.py\n\n" +
			"aa000054 REPLACE\naa000054 snippet\n  x = 1\n" +
			"aa000054 content\n  x = 1\n",
		expectUnchanged: []string{"m.py"},
	},
	{
		// Applying a re-indented REPLACE twice: the second run must be a no-op.
		name:  "c55_reindented_replace_rerun_is_skipped",
		files: map[string]string{"m.py": "def f():\n    a = 2\n"},
		patch: "aa000055 AP 3.2\n\naa000055 FILE\nm.py\n\n" +
			"aa000055 REPLACE\naa000055 snippet\na = 1\naa000055 content\na = 2\n",
		expectUnchanged: []string{"m.py"},
		expectStdout:    []string{"already applied"},
	},
}

func runAPCase(t *testing.T, c apCase) {
	t.Helper()
	dir := t.TempDir()

	for rel, content := range c.files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("setup: mkdir for %q: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("setup: write %q: %v", rel, err)
		}
	}

	patchPath := filepath.Join(dir, "_case.ap")
	if err := os.WriteFile(patchPath, []byte(c.patch), 0o600); err != nil {
		t.Fatalf("setup: write patch: %v", err)
	}

	// Some cases assert on what the patcher prints (tolerant notes,
	// structural warnings), which only happens when it is not silenced.
	wantStdout := len(c.expectStdout) > 0
	var buf bytes.Buffer
	result := Apply(patchPath, dir, Options{Strict: c.strict, Silent: !wantStdout, Out: &buf})

	expectedStatus := c.expectStatus
	if expectedStatus == "" {
		expectedStatus = StatusSuccess
	}
	if result.Status != expectedStatus {
		detail := ""
		if result.Error != nil {
			detail = fmt.Sprintf(" (%s: %s)", result.Error.Code, result.Error.Message)
		}
		t.Fatalf("expected status %s, got %s%s", expectedStatus, result.Status, detail)
	}

	if c.expectError != "" {
		var actual ErrCode
		if result.Error != nil {
			actual = result.Error.Code
		}
		if actual != c.expectError {
			t.Fatalf("expected error %s, got %s", c.expectError, actual)
		}
	}

	for rel, expected := range c.expectFiles {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected file %q does not exist: %v", rel, err)
		}
		if string(b) != expected {
			t.Fatalf("content of %q differs:\n--- expected ---\n%q\n--- actual ---\n%q", rel, expected, string(b))
		}
	}

	for _, rel := range c.expectUnchanged {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("unchanged file %q missing: %v", rel, err)
		}
		if string(b) != c.files[rel] {
			t.Fatalf("file %q was modified but should not have been", rel)
		}
	}

	for _, rel := range c.expectMissing {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("file %q should not exist", rel)
		}
	}

	printed := buf.String()
	for _, needle := range c.expectStdout {
		if !strings.Contains(printed, needle) {
			t.Fatalf("expected %q in the patcher output, got:\n%s", needle, printed)
		}
	}

	if len(c.expectMD) > 0 {
		mdPath := filepath.Join(dir, "afailed.md")
		b, err := os.ReadFile(mdPath)
		if err != nil {
			t.Fatalf("afailed.md was not created: %v", err)
		}
		md := string(b)
		for _, needle := range c.expectMD {
			if !strings.Contains(md, needle) {
				t.Fatalf("afailed.md does not mention %q; content:\n%s", needle, md)
			}
		}
	}
}

func TestCases(t *testing.T) {
	for _, c := range apCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			runAPCase(t, c)
		})
	}
}
