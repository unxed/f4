package ap

import (
	"fmt"
	"strings"
)

// applyOneModification applies a single modification of change against
// *workingContent (in place, via the pointer), in the context of the other
// per-file state accumulated across the whole modification loop
// (dirtyRegions, consumedLog).
//
// Return values:
//   - res, stop: when stop is true, res is the final Result for the whole
//     Apply call (a strict-mode fatal error) and the caller must return it
//     immediately.
//   - failure: non-nil for a tolerant-mode failure (goes into this pass's
//     retry list / eventually the LLM report).
//   - progressed: true if this modification actually mutated the file (or
//     created it) - mirrors the reference's `made_progress = True`.
//   - newLastEnd: the sequential cursor position for the *next*
//     modification of this file; equals lastModEndPos unchanged unless the
//     reference explicitly updates it for this case.
func (e *engine) applyOneModification(
	change *FileChange, mod *Modification, modIdx int, relativePath, filePath string,
	isExplicitDir, fileExisted bool, originalContent string, workingContent *string,
	initialContent string, dirtyRegions *[]span, consumedLog *[]consumedEntry,
	allowRepeat bool, lastModEndPos, passNumber int, terminalOpPlanned *bool,
) (res *Result, failure *modFailure, progressed bool, newLastEnd int, stop bool) {

	skip := func() (*Result, *modFailure, bool, int, bool) { return nil, nil, false, lastModEndPos, false }
	skipWithPos := func(pos int) (*Result, *modFailure, bool, int, bool) { return nil, nil, false, pos, false }
	fail := func(aerr *AppError) (*Result, *modFailure, bool, int, bool) {
		return nil, &modFailure{modIdx, mod, aerr}, false, lastModEndPos, false
	}
	fatalOrFail := func(aerr *AppError) (*Result, *modFailure, bool, int, bool) {
		if e.strict {
			// Record before calling fatal(): fatal() snapshots
			// e.modResults into the Result it returns, so the failing
			// modification itself must already be in it.
			e.recordModResult(relativePath, modIdx, mod, ModFailed, aerr)
			return e.fatal(StatusFailed, relativePath, true, modIdx, true, aerr), nil, false, 0, true
		}
		return fail(aerr)
	}
	success := func(pos int) (*Result, *modFailure, bool, int, bool) { return nil, nil, true, pos, false }

	passStr := ""
	if passNumber > 1 {
		passStr = fmt.Sprintf(" (Pass %d)", passNumber)
	}

	action := mod.Action
	contentToAdd := cleanLines(mod.Content)

	if action == "REPLACE" || action == "INSERT_AFTER" || action == "INSERT_BEFORE" || action == "RECREATE" {
		if mod.Content == nil {
			aerr := &AppError{Code: ErrMissingContent,
				Message: fmt.Sprintf("Action '%s' requires 'content' directive.", action), Context: map[string]any{}}
			return fatalOrFail(aerr)
		}
		if (action == "REPLACE" || action == "RECREATE") && derefOr(contentToAdd, "") == "" {
			truncated := mod.eofValue == "content"
			if truncated || e.strict {
				code := ErrEmptyReplace
				msg := fmt.Sprintf("%s with empty content is not allowed in strict mode. Use DELETE instead.", action)
				if truncated {
					code = ErrPatchTruncated
					msg = "The patch ends with an empty 'content' block, so it cannot be told apart from a " +
						"truncated answer. Finish the patch, or close it with an 'END' directive."
				}
				return fatalOrFail(&AppError{Code: code, Message: msg, Context: map[string]any{}})
			}
			if action == "REPLACE" {
				action = "DELETE"
				contentToAdd = nil
				e.printf("  [TOLERANT] Mod #%d: empty 'content'; treating REPLACE as DELETE.\n", modIdx+1)
			}
		}
	}

	if action == "RECREATE" {
		newVal := derefOr(contentToAdd, "")
		if *workingContent == newVal {
			e.reportIdempotencySkip("RECREATE content already matches.")
			return skip()
		}
		*workingContent = newVal
		if newVal != "" {
			*dirtyRegions = []span{{0, len(newVal)}}
		} else {
			*dirtyRegions = nil
		}
		e.printf("  + SUCCESS: Mod #%d (RECREATE) applied%s.\n", modIdx+1, passStr)
		return success(len(newVal))
	}

	if action == "" {
		return fatalOrFail(&AppError{Code: ErrInvalidModification, Message: "'action' is required.", Context: map[string]any{}})
	}

	snippetVal := cleanLines(mod.Snippet)
	snippetTail := cleanLines(mod.SnippetTail)
	anchorVal := cleanLines(mod.Anchor)

	if !e.strict && derefOr(snippetVal, "") == "" && derefOr(anchorVal, "") != "" &&
		(action == "REPLACE" || action == "DELETE" || action == "INSERT_AFTER" || action == "INSERT_BEFORE") {
		snippetVal = anchorVal
		anchorVal = nil
		e.printf("  [TOLERANT] Mod #%d: Missing 'snippet'. Using 'anchor' as snippet.\n", modIdx+1)
	}

	// === SAFE CREATE (file or directory) ===
	if action == "CREATE" {
		isDirCreate := contentToAdd == nil || (derefOr(contentToAdd, "") == "" && isExplicitDir)
		if isDirCreate {
			if isDirPath(filePath) {
				e.reportIdempotencySkip("Directory already exists.")
				// NOTE: the reference terminates all further processing of
				// this file block here; we simply skip this one
				// modification and let the rest run, which is strictly
				// more useful and is not observably different for any
				// patch that does not keep issuing modifications against a
				// directory it just (no-op) "created".
				return skip()
			}
			if pathExists(filePath) {
				return fatalOrFail(&AppError{Code: ErrPathIsFile, Message: "Cannot create directory, a file exists at the path."})
			}
			e.writePlan = append(e.writePlan, writeOp{kind: opCreateDir, path: filePath, relPath: relativePath})
			*terminalOpPlanned = true
			*workingContent = ""
			e.printf("  + SUCCESS: Mod #%d (CREATE) applied%s.\n", modIdx+1, passStr)
			return success(lastModEndPos)
		}

		if fileExisted && !isDirPath(filePath) {
			normalizedExisting := normalizeCreateCompare(originalContent)
			normalizedNew := normalizeCreateCompare(derefOr(contentToAdd, ""))
			if normalizedExisting == normalizedNew {
				e.reportIdempotencySkip("File exists with matching content.")
				return skip()
			}
			if strings.TrimSpace(originalContent) != "" {
				return fatalOrFail(&AppError{Code: ErrFileExists, Message: "Target file exists and is not empty."})
			}
			// else: existing file is empty, fall through and overwrite it.
		}

		*workingContent = normalizeToLF(derefOr(contentToAdd, ""))
		e.printf("  + SUCCESS: Mod #%d (CREATE) applied%s.\n", modIdx+1, passStr)
		return success(lastModEndPos)
	}

	// Heuristic: snippet_tail identical to content, or to snippet, or a
	// suffix of snippet - the model confused range boundaries.
	if derefOr(snippetVal, "") != "" && derefOr(snippetTail, "") != "" && derefOr(contentToAdd, "") != "" {
		if normalizeCreateCompare(derefOr(snippetTail, "")) == normalizeCreateCompare(derefOr(contentToAdd, "")) {
			snippetTail = nil
		}
	}
	if snippetTail != nil && derefOr(snippetVal, "") != "" &&
		strings.TrimSpace(derefOr(snippetVal, "")) == strings.TrimSpace(derefOr(snippetTail, "")) {
		snippetTail = nil
	}
	if snippetTail != nil && derefOr(snippetVal, "") != "" &&
		strings.HasSuffix(strings.TrimSpace(derefOr(snippetVal, "")), strings.TrimSpace(derefOr(snippetTail, ""))) {
		snippetTail = nil
	}

	var targetStart, targetEnd int
	var haveTarget bool
	var aerr *AppError

	switch {
	case snippetTail != nil:
		switch {
		case snippetVal == nil:
			aerr = &AppError{Code: ErrInvalidModification, Message: "Range requires 'snippet'.", Context: map[string]any{}}
		case action != "REPLACE" && action != "DELETE" && action != "INSERT_AFTER" && action != "INSERT_BEFORE":
			aerr = &AppError{Code: ErrInvalidModification,
				Message: fmt.Sprintf("Action '%s' does not support range.", action), Context: map[string]any{}}
		case (action == "INSERT_AFTER" || action == "INSERT_BEFORE") && e.strict:
			aerr = &AppError{Code: ErrInvalidModification,
				Message: fmt.Sprintf("Action '%s' is a point operation and MUST NOT carry 'snippet_tail'.", action),
				Context: map[string]any{}}
		default:
			if action == "INSERT_AFTER" || action == "INSERT_BEFORE" {
				where := "end"
				if action == "INSERT_BEFORE" {
					where = "start"
				}
				e.printf("  [TOLERANT] Mod #%d: '%s' is a point action; inserting at the %s of the given range.\n",
					modIdx+1, action, where)
			}
			var startRangeBegin, startRangeEnd int
			if strings.TrimSpace(derefOr(snippetVal, "")) == "^" {
				startRangeBegin, startRangeEnd = 0, 0
			} else {
				sp, ep, serr := findTargetInContent(*workingContent, anchorVal, snippetVal, lastModEndPos, allowRepeat, *dirtyRegions, false)
				if serr != nil {
					aerr = serr
				} else {
					startRangeBegin, startRangeEnd = sp, ep
				}
			}
			if aerr == nil {
				if strings.TrimSpace(derefOr(snippetTail, "")) == "$" {
					targetStart, targetEnd, haveTarget = startRangeBegin, len(*workingContent), true
				} else {
					endOccurrences := smartFind((*workingContent)[startRangeEnd:], derefOr(snippetTail, ""))
					if len(endOccurrences) > 0 {
						targetStart = startRangeBegin
						targetEnd = startRangeEnd + endOccurrences[0][1]
						haveTarget = true
					} else {
						var before []occurrence
						if !e.strict {
							before = smartFind((*workingContent)[:startRangeBegin], derefOr(snippetTail, ""))
						}
						if len(before) > 0 {
							e.printf("  [TOLERANT] Mod #%d: 'snippet_tail' occurs BEFORE 'snippet'. "+
								"Treating the pair as a reversed range.\n", modIdx+1)
							targetStart = before[len(before)-1][0]
							targetEnd = startRangeEnd
							haveTarget = true
						} else {
							aerr = &AppError{Code: ErrSnippetTailNotFound, Message: "End snippet not found.",
								Context: map[string]any{"snippet": derefOr(snippetVal, ""), "snippet_tail": derefOr(snippetTail, "")}}
						}
					}
				}
			}
		}
	case snippetVal != nil:
		sp, ep, serr := findTargetInContent(*workingContent, anchorVal, snippetVal, lastModEndPos, allowRepeat, *dirtyRegions, false)
		if serr != nil {
			aerr = serr
		} else {
			targetStart, targetEnd, haveTarget = sp, ep, true
		}
	default:
		if action != "CREATE" {
			aerr = &AppError{Code: ErrInvalidModification, Message: "Modification requires locators.", Context: map[string]any{}}
		}
	}

	if aerr != nil {
		if action == "DELETE" && (aerr.Code == ErrSnippetNotFound || aerr.Code == ErrAnchorNotFound) {
			e.reportIdempotencySkip("Snippet to delete is already gone.")
			return skip()
		}
		if action == "REPLACE" && (aerr.Code == ErrSnippetNotFound || aerr.Code == ErrAnchorNotFound || aerr.Code == ErrSnippetTailNotFound) {
			_, _, cerr := findTargetInContent(*workingContent, anchorVal, contentToAdd, 0, false, *dirtyRegions, true)
			if cerr == nil {
				e.reportIdempotencySkip("Snippet not found, but replacement content exists.")
				return skip()
			}
		}

		if aerr.Code == ErrSnippetNotFound || aerr.Code == ErrAnchorNotFound || aerr.Code == ErrSnippetTailNotFound {
			var probe string
			switch aerr.Code {
			case ErrAnchorNotFound:
				probe = derefOr(anchorVal, "")
			case ErrSnippetTailNotFound:
				probe = derefOr(snippetTail, "")
			default:
				probe = derefOr(snippetVal, "")
			}
			culprit := -2
			if probe != "" {
				for _, c := range *consumedLog {
					if c.Idx != modIdx && c.Removed != "" && len(smartFind(c.Removed, probe)) > 0 {
						culprit = c.Idx
						break
					}
				}
				if culprit == -2 && len(smartFind(initialContent, probe)) > 0 && len(smartFind(*workingContent, probe)) == 0 {
					culprit = -1
				}
			}
			if culprit != -2 {
				where := "an earlier modification"
				if culprit >= 0 {
					where = fmt.Sprintf("modification #%d", culprit+1)
				}
				ctx := map[string]any{"snippet": derefOr(snippetVal, ""), "anchor": derefOr(anchorVal, "")}
				if culprit >= 0 {
					ctx["removed_by_mod"] = culprit + 1
				} else {
					ctx["removed_by_mod"] = nil
				}
				aerr = &AppError{
					Code: ErrLocatorConsumed,
					Message: fmt.Sprintf("The locator existed in the original file but %s of the same file removed "+
						"it. Modifications are applied in order, each seeing the result of the previous ones.", where),
					Context: ctx,
				}
			}
		}

		if aerr.Context == nil {
			aerr.Context = map[string]any{}
		}
		aerr.Context["action"] = action
		return fatalOrFail(aerr)
	}

	if !haveTarget {
		// Defensive: should be unreachable (aerr would be set otherwise).
		return fail(&AppError{Code: ErrInvalidModification, Message: "Modification requires locators.", Context: map[string]any{}})
	}

	// === SCOPE EXPANSION ===
	if mod.ScopeEnd != 0 {
		expanded, ok := resolveScopeEnd(*workingContent, targetStart, targetEnd)
		if !ok {
			e.printf("  [TOLERANT] Mod #%d: 'scope_end' requested but the snippet does not open a block; ignoring it.\n", modIdx+1)
		} else {
			targetEnd = maxInt(targetEnd, expanded)
		}
	}

	// === BLANK LINE INCLUSION ===
	for _, spec := range []struct {
		count     int
		direction int
	}{{mod.IncludeLeadingBlankLines, -1}, {mod.IncludeTrailingBlankLines, 1}} {
		if spec.count <= 0 {
			continue
		}
		pos := targetStart
		if spec.direction == 1 {
			pos = targetEnd
		}
		for iter := 0; iter < spec.count; iter++ {
			var nextNewline int
			if spec.direction == -1 {
				s, en := pySliceBounds(len(*workingContent), 0, pos-1)
				rel := strings.LastIndex((*workingContent)[s:en], "\n")
				if rel == -1 {
					nextNewline = -1
				} else {
					nextNewline = s + rel
				}
			} else {
				p := clampInt(pos, 0, len(*workingContent))
				idx := strings.Index((*workingContent)[p:], "\n")
				if idx == -1 {
					nextNewline = -1
				} else {
					nextNewline = p + idx
				}
			}
			if nextNewline == -1 {
				var seg string
				if spec.direction == -1 {
					seg = (*workingContent)[:clampInt(pos, 0, len(*workingContent))]
				} else {
					seg = (*workingContent)[clampInt(pos, 0, len(*workingContent)):]
				}
				if strings.TrimSpace(seg) == "" {
					if spec.direction == -1 {
						pos = 0
					} else {
						pos = len(*workingContent)
					}
				}
				break
			}
			var lineContent string
			if spec.direction == -1 {
				lineContent = (*workingContent)[nextNewline+1 : clampInt(pos, nextNewline+1, len(*workingContent))]
			} else {
				lineContent = (*workingContent)[clampInt(pos, 0, nextNewline):nextNewline]
			}
			if strings.TrimSpace(lineContent) == "" {
				pos = nextNewline + 1
			} else {
				break
			}
		}
		if spec.direction == -1 {
			targetStart = pos
		} else {
			targetEnd = pos
		}
	}

	// === NESTING GUARD ===
	if (action == "INSERT_AFTER" || action == "INSERT_BEFORE") && derefOr(contentToAdd, "") != "" {
		probe := targetEnd
		if action == "INSERT_BEFORE" {
			probe = targetStart
		}
		corrected, ok := topLevelInsertCorrection(*workingContent, probe, derefOr(contentToAdd, ""), action == "INSERT_AFTER")
		if ok && corrected != probe {
			if e.strict {
				return fatalOrFail(&AppError{
					Code: ErrNestingMismatch,
					Message: fmt.Sprintf("'%s' would place a self-contained top-level block inside another block. "+
						"Use a locator on the surrounding declaration with 'scope_end 1', or target the "+
						"neighbouring top-level declaration instead.", action),
					Context: map[string]any{"snippet": derefOr(snippetVal, "")},
				})
			}
			where := "end"
			if action == "INSERT_BEFORE" {
				where = "start"
			}
			e.printf("  [TOLERANT] Mod #%d: insertion point is inside a block while 'content' is a self-contained "+
				"top-level block; moving it to the %s of the enclosing block.\n", modIdx+1, where)
			needsPad := corrected >= 2 && (*workingContent)[corrected-1] == '\n' && (*workingContent)[corrected-2] != '\n'
			if action == "INSERT_AFTER" {
				targetEnd = corrected
				targetStart = minInt(targetStart, targetEnd)
				if needsPad {
					v := "\n" + derefOr(contentToAdd, "")
					contentToAdd = &v
				}
			} else {
				targetStart = corrected
				targetEnd = maxInt(targetEnd, targetStart)
				if corrected > 0 && !strings.HasSuffix((*workingContent)[:corrected], "\n\n") {
					v := derefOr(contentToAdd, "") + "\n"
					contentToAdd = &v
				}
			}
		}
	}

	// === IDEMPOTENCY: is the requested state already there? ===
	// A REPLACE equal to its region up to whitespace is either already
	// applied or changes only indentation (dedent/indent); see
	// whitespaceOnlyReplacement.
	var replaceText *string
	if action == "REPLACE" && normalizeBlock((*workingContent)[targetStart:targetEnd]) == normalizeBlock(derefOr(contentToAdd, "")) {
		region := (*workingContent)[targetStart:targetEnd]
		var oldText *string
		if snippetTail == nil && snippetVal != nil && normalizeBlock(*snippetVal) == normalizeBlock(region) {
			oldText = snippetVal
		}
		wc, ts, te := *workingContent, targetStart, targetEnd
		text, apply := whitespaceOnlyReplacement(region, oldText, derefOr(contentToAdd, ""), func(n string) string {
			return reindentContent(wc, ts, te, n, action, e.strict, func(string) {})
		})
		if !apply {
			e.reportIdempotencySkip("REPLACE content already present.")
			return skipWithPos(targetEnd)
		}
		e.printf("  ~ Mod #%d: 'content' differs from the target only by indentation; "+
			"applying it as an indentation change.\n", modIdx+1)
		replaceText = &text
	}
	switch {
	case action == "INSERT_AFTER" && strings.HasPrefix(normalizeBlock((*workingContent)[targetEnd:]), normalizeBlock(derefOr(contentToAdd, ""))):
		e.reportIdempotencySkip("INSERT_AFTER content already present.")
		alreadyThere := smartFind((*workingContent)[targetEnd:], derefOr(contentToAdd, ""))
		pos := targetEnd
		if len(alreadyThere) > 0 {
			pos = targetEnd + alreadyThere[0][1]
		}
		return skipWithPos(pos)
	case action == "INSERT_BEFORE" && strings.HasSuffix(normalizeBlock((*workingContent)[:targetStart]), normalizeBlock(derefOr(contentToAdd, ""))):
		e.reportIdempotencySkip("INSERT_BEFORE content already present.")
		return skipWithPos(targetStart)
	}

	if (action == "REPLACE" || action == "DELETE") && targetEnd > targetStart {
		*consumedLog = append(*consumedLog, consumedEntry{modIdx, (*workingContent)[targetStart:targetEnd]})
	}

	if action == "DELETE" {
		*workingContent = (*workingContent)[:targetStart] + (*workingContent)[targetEnd:]
		*dirtyRegions = shiftDirty(*dirtyRegions, targetStart, targetEnd, 0)
	}

	indentedContent := derefOr(contentToAdd, "")
	if (action == "REPLACE" || action == "INSERT_AFTER" || action == "INSERT_BEFORE") && derefOr(contentToAdd, "") != "" {
		indentWarnFn := func(msg string) { e.printf("  [TOLERANT] Mod #%d: %s\n", modIdx+1, msg) }
		if replaceText != nil {
			indentedContent = *replaceText
		} else {
			indentedContent = reindentContent(*workingContent, targetStart, targetEnd, derefOr(contentToAdd, ""), action, e.strict, indentWarnFn)
		}
		originalHadTrailingNewline := targetEnd > targetStart && targetEnd-1 < len(*workingContent) && (*workingContent)[targetEnd-1] == '\n'
		if action == "INSERT_AFTER" || action == "INSERT_BEFORE" || (action == "REPLACE" && originalHadTrailingNewline) {
			if !strings.HasSuffix(indentedContent, "\n") {
				indentedContent += "\n"
			}
		}
	}

	switch action {
	case "REPLACE":
		*workingContent = (*workingContent)[:targetStart] + indentedContent + (*workingContent)[targetEnd:]
		*dirtyRegions = shiftDirty(*dirtyRegions, targetStart, targetEnd, len(indentedContent))
	case "INSERT_AFTER":
		*workingContent = (*workingContent)[:targetEnd] + indentedContent + (*workingContent)[targetEnd:]
		*dirtyRegions = shiftDirty(*dirtyRegions, targetEnd, targetEnd, len(indentedContent))
	case "INSERT_BEFORE":
		*workingContent = (*workingContent)[:targetStart] + indentedContent + (*workingContent)[targetStart:]
		*dirtyRegions = shiftDirty(*dirtyRegions, targetStart, targetStart, len(indentedContent))
	}

	var newCursor int
	switch action {
	case "REPLACE":
		newCursor = targetStart + len(indentedContent)
	case "INSERT_AFTER":
		newCursor = targetEnd + len(indentedContent)
	case "INSERT_BEFORE":
		newCursor = targetStart + len(indentedContent)
	case "DELETE":
		newCursor = targetStart
	}

	e.printf("  + SUCCESS: Mod #%d (%s) applied%s.\n", modIdx+1, action, passStr)
	return success(newCursor)
}
