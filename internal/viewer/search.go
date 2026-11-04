package viewer

import (
	"context"
	"io"
	"strings"

	"github.com/unxed/f4/internal/textsearch"
)

// The viewer's own search over its backend. It sat in cmd/f4's action table
// because that is where the F7 handler is; nothing in it is an action, and
// every argument is a viewer type.

type SearchOptions struct {
	CaseSensitive bool
	Reverse       bool
	Regexp        bool
	WholeWord     bool
}

func SearchOffset(ctx context.Context, backend *ViewerBackend, pattern string, start int64, reverse bool, progress func(int)) int64 {
	offset, _, _ := SearchMatch(ctx, backend, pattern, start, SearchOptions{Reverse: reverse}, progress)
	return offset
}

// SearchMatch returns the byte offset and byte length of the next match.
// Literal searches retain the viewer's streaming behavior; regex and
// whole-word searches use one snapshot so matches crossing read boundaries
// have the same semantics as editor search.
func SearchMatch(ctx context.Context, backend *ViewerBackend, pattern string, start int64, options SearchOptions, progress func(int)) (int64, int, error) {
	if backend == nil || pattern == "" {
		return -1, 0, nil
	}
	fileSize := backend.Size()
	if fileSize <= 0 {
		return -1, 0, nil
	}
	if start < 0 {
		start = 0
	}
	if start > fileSize {
		start = fileSize
	}

	if options.Regexp || options.WholeWord {
		data, err := readSearchData(ctx, backend, progress)
		if err != nil {
			return -1, 0, err
		}
		if int64(len(data)) < start {
			start = int64(len(data))
		}
		found, matchLen, err := textsearch.FindMatch(data, pattern, options.CaseSensitive, options.Reverse, options.Regexp, options.WholeWord, false, int(start))
		return int64(found), matchLen, err
	}

	patternLower := strings.ToLower(pattern)
	chunkSize := int64(256 * 1024)
	if int64(len(patternLower)) > chunkSize {
		chunkSize = int64(len(patternLower))
	}
	overlap := int64(len(patternLower) - 1)

	if options.Reverse {
		if !options.CaseSensitive {
			if at, searched := backend.SearchBefore(ctx, pattern, start); searched {
				return at, len(pattern), nil
			}
		}
		end := start
		for end > 0 {
			if ctx.Err() != nil {
				return -1, 0, ctx.Err()
			}
			begin := end - chunkSize
			if begin < 0 {
				begin = 0
			}
			if progress != nil {
				progress(int(((fileSize - end) * 100) / fileSize))
			}
			data, err := backend.readSearchAt(ctx, begin, int(end-begin))
			if err != nil || len(data) == 0 {
				return -1, 0, nil
			}
			if idx, matchLen, matchErr := textsearch.FindMatch(data, pattern, options.CaseSensitive, true, false, false, false, len(data)); matchErr != nil {
				return -1, 0, matchErr
			} else if idx >= 0 {
				return begin + int64(idx), matchLen, nil
			}
			if begin == 0 {
				break
			}
			end = begin + overlap
		}
		return -1, 0, nil
	}

	if !options.CaseSensitive {
		if at, searched := backend.SearchFrom(ctx, pattern, start); searched {
			return at, len(pattern), nil
		}
	}
	current := start
	for current < fileSize {
		if ctx.Err() != nil {
			return -1, 0, ctx.Err()
		}
		if progress != nil {
			progress(int((current * 100) / fileSize))
		}
		data, err := backend.readSearchAt(ctx, current, int(chunkSize))
		if err != nil || len(data) == 0 {
			break
		}
		if idx, matchLen, matchErr := textsearch.FindMatch(data, pattern, options.CaseSensitive, false, false, false, false, 0); matchErr != nil {
			return -1, 0, matchErr
		} else if idx >= 0 {
			return current + int64(idx), matchLen, nil
		}
		advance := int64(len(data)) - overlap
		if advance < 1 {
			advance = 1
		}
		current += advance
	}
	return -1, 0, nil
}

// readSearchData materializes the decoded viewer stream for searches
// whose match rules can span arbitrary read boundaries. viewer.ViewerBackend still
// fetches it in bounded windows, so remote VFSes remain cancelable and do not
// allocate one request per file chunk.
func readSearchData(ctx context.Context, backend *ViewerBackend, progress func(int)) ([]byte, error) {
	fileSize := backend.Size()
	capacity := 0
	if fileSize <= int64(int(^uint(0)>>1)) {
		capacity = int(fileSize)
	}
	data := make([]byte, 0, capacity)
	const chunkSize = int64(256 * 1024)
	for current := int64(0); current < fileSize; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		length := chunkSize
		if remaining := fileSize - current; remaining < length {
			length = remaining
		}
		chunk, err := backend.readSearchAt(ctx, current, int(length))
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(chunk) == 0 {
			break
		}
		data = append(data, chunk...)
		current += int64(len(chunk))
		if progress != nil {
			progress(int((current * 100) / fileSize))
		}
	}
	return data, nil
}
