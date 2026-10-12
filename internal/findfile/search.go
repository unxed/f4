package findfile

import (
	"context"
	"errors"
	"fmt"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// run owns the walk and calls emit/progress only on its worker goroutine.
func run(ctx context.Context, v vfs.VFS, root, mask, text string, options Options, emit func(vfs.FoundEntry), progress func(vfs.FindProgress)) error {
	if len(options.SelectedFolders) == 0 {
		return runDirectory(ctx, v, root, mask, text, options, emit, progress)
	}
	_, excludes, err := splitFindMasks(mask)
	if err != nil {
		return fmt.Errorf("invalid file mask: %w", err)
	}
	if _, err := newFindTextMatcher(text, options); err != nil {
		return fmt.Errorf("invalid search pattern: %w", err)
	}
	roots := make([]string, 0, len(options.SelectedFolders))
	seen := make(map[string]bool)
	for _, dir := range options.SelectedFolders {
		if seen[dir] || findFileMaskMatches(v.Base(dir), excludes, !options.CaseSensitive) {
			continue
		}
		seen[dir] = true
		roots = append(roots, dir)
	}
	p := vfs.FindProgress{Path: root, DirectoryTotalKnown: true, TotalDirs: int64(len(roots))}
	progress(p)
	for _, dir := range roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		scanned, found := p.Scanned, p.Found
		err := runDirectory(ctx, v, dir, mask, text, options, emit, func(current vfs.FindProgress) {
			p.Path = current.Path
			p.Scanned = scanned + current.Scanned
			p.Found = found + current.Found
			progress(p)
		})
		if err != nil {
			return err
		}
		p.CompletedDirs++
		progress(p)
	}
	return ctx.Err()
}

func runDirectory(ctx context.Context, v vfs.VFS, root, mask, text string, options Options, emit func(vfs.FoundEntry), progress func(vfs.FindProgress)) error {
	masks, excludes, err := splitFindMasks(mask)
	if err != nil {
		return fmt.Errorf("invalid file mask: %w", err)
	}
	matcher, err := newFindTextMatcher(text, options)
	if err != nil {
		return fmt.Errorf("invalid search pattern: %w", err)
	}
	if finder, ok := v.(vfs.StreamingFileFinder); ok && options.usesDefaultSearchEngine() && len(excludes) == 0 {
		err := finder.FindFilesStream(ctx, root, vfs.FindQuery{
			Masks: masks, Text: text, IgnoreCase: !options.CaseSensitive,
			Regex: options.Regex, Progress: progress,
		}, emit)
		if !errors.Is(err, vfs.ErrFindOptionsUnsupported) || ctx.Err() != nil {
			return err
		}
		vtui.DebugLog("FIND: streaming finder unavailable, walking instead: %v", err)
	}

	var p vfs.FindProgress
	var walk func(string, bool) error
	walk = func(dir string, isRoot bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.Path = dir
		progress(p)
		var children []string
		err := v.ReadDir(ctx, dir, func(chunk []vfs.VFSItem) {
			for _, item := range chunk {
				if ctx.Err() != nil {
					return
				}
				if item.Name == "." || item.Name == ".." {
					continue
				}
				p.Scanned++
				if findFileMaskMatches(item.Name, excludes, !options.CaseSensitive) {
					continue
				}
				path := v.Join(dir, item.Name)
				if item.IsSymlink {
					if !options.FindSymlinks {
						continue
					}
					item.IsDir = false
				}
				matched := findFileMaskMatches(item.Name, masks, !options.CaseSensitive)
				if item.IsDir {
					children = append(children, path)
					matched = matched && options.FindFolders && text == ""
				} else if matched && matcher != nil {
					matched = fileContainsTextWithMatcher(ctx, v, path, matcher)
				}
				if matched && ctx.Err() == nil {
					p.Found++
					emit(vfs.FoundEntry{Path: path, Item: item})
				}
				p.Path = path
				progress(p)
			}
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if isRoot {
				return err
			}
			vtui.DebugLog("FIND: skipping directory %q: %v", dir, err)
			return nil
		}
		if isRoot {
			p.DirectoryTotalKnown = true
			p.TotalDirs = int64(len(children))
			progress(p)
		}
		for _, child := range children {
			if err := walk(child, false); err != nil {
				return err
			}
			if isRoot {
				p.CompletedDirs++
				progress(p)
			}
		}
		return ctx.Err()
	}
	return walk(root, true)
}
