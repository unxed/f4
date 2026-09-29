# Nested archives

f4 can open an archive member as another read-only VFS. When the parent is
already a virtual file system, `plugins/archive` first tries a generic
reader-backed path. The next archive provider receives a `ReadAt`/stream view
of the member instead of an automatically materialized copy of the whole
intermediary.

The first implementation is deliberately a vertical slice:

- ZIP and compressed TAR (`.tar.gz`, `.tar.xz`, `.tar.bz2`, `.tar.zst`, and
  other compressed-TAR formats registered by `github.com/unxed/archives`) can
  compose in either order and at more than one level;
- the implementation is generic over the registered extraction API, rather
  than a ZIP/TAR pair of special cases;
- ordinary top-level archives, remote-file materialization, disk extraction,
  and the existing password-specific archive path remain in place.

The reader-backed path does not create an archive-sized temporary file for
each intermediary. It requires a known source size. The generic archive
filesystem may cache directory metadata in memory, and `ReadAt` on a
sequential or compressed member replays from that member's beginning; this
keeps storage bounded but can make distant reads expensive. It is not an
indexing or random-access optimization.

Unsupported or format-specific cases remain explicit limits of this slice:
solid/password-protected backends and formats that require a local filename
may use the existing materialization fallback. Observer input is
reader-backed already, so an Observer container can sit in the composition
chain. Observer API v6 still exposes an item only through `ExtractItem`, which
writes the complete item to the host extraction directory; an Observer item
therefore remains a bounded, cleaned-up temporary-file boundary until the ABI
offers an item stream. Cancellation and read errors still travel through the
parent VFS read calls; callers should close nested VFSes and member handles.

The next step is to make the fallback boundary explicit and extend
format-specific adapters/indexes where the generic stream API cannot provide
the desired access, while preserving this provider-neutral composition point.
