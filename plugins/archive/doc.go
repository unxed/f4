// Package archive exposes f4's read-only and writable archive VFS provider.
//
// Nested archive opens use a reader-backed path when the parent is already a
// virtual VFS. The path adapts the parent's ReadAtCloser to the generic
// github.com/unxed/archives reader API, so ZIP and compressed TAR layers can
// be opened in arbitrary order without first copying every intermediary to a
// temporary file. The adapter keeps memory bounded by the decoder and caller
// buffers; a ReadAt on a sequential member replays that member from its start,
// so a far offset may cost proportional I/O and decompression work.
//
// This is a first composition slice, not a promise that every archive backend
// is streamable. A source with no known size, a format that cannot be opened by
// the generic reader API, or a format-specific operation that needs passwords
// or a local path continues through the existing materialization path. The
// ordinary top-level archive path is unchanged. See docs/NESTED_ARCHIVES.md
// for the user-visible limits and the Observer boundary.
package archive
