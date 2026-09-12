# Issue #915 solution review — the overall bar of Shift-F3 archive testing

## What the bar actually measured

`testArchiveOnce` counted the bytes consumed from the *compressed* archive
stream (`archiveTestingReader`, and the `ReadAt` wrapper around it) and divided
them by the size of the archive file. Recording every `UpdateTransfer` of a
testing pass over four 512 KB members shows what that produces:

| format | overall bar |
| --- | --- |
| zip, stored members | climbs 4% → 100%, right by coincidence — stored members are their own compressed size |
| 7z | **100% from the first update**, with the text `Total: 2.6 KB / 1.9 KB` |
| tar.gz | jumps to 32% on the first read, then moves in coarse steps |

The 7z number is not a rounding artefact. `sevenzip` re-reads and seeks around
its input, so the accumulated read count passes the size of the file while the
first member is still being checked, and the percentage saturates for the rest
of the pass. The reporter shows the bar whenever the percentage is `>= 0`, so
the user gets a full bar that never moves — the reported symptom.

Neither figure is what the issue asks for, which is tested volume against the
total volume of the files in the archive.

## Candidate 1: measure the position in the archive file instead of bytes read

Replacing the accumulated count with the highest offset reached would stop the
overrun for 7z, but 7z reads its input out of order — the highest offset is
reached early and the bar still sticks. It also keeps measuring the compressed
container rather than the data being verified, so the "Total:" text keeps
naming figures the user cannot relate to the archive contents.

## Candidate 2: sum the member sizes during the pass itself

The denominator is only complete once the last member has been handled, which
is the moment the bar stops being needed. It would turn the overall bar into a
second copy of the current-file bar.

## Candidate 3: take the denominator from the archive directory (selected)

Zip keeps a central directory, 7z a header, and RAR a chain of file block
headers walked by skipping over packed data: each can be enumerated without
starting a decompressor. A listing pass over the format before testing
therefore yields the exact uncompressed volume for the price of a header read,
and the bar then shows bytes verified against that total — the definition in
the issue.

Tar has no directory, so listing a tar or any compressed-tar combination means
decoding the whole stream. Paying that twice to label a progress bar is not
worth it, and those formats are read strictly front to back, which makes the
existing stream-position measure monotonic and serviceable. They keep it.

## Behaviour that has to survive

- **A damaged archive still gets tested.** Reporting on damage is what the
  operation is for, so a listing pass that fails leaves the totals unknown and
  the testing pass runs anyway; only the bar gets coarser.
- **Password retries still work.** A password error from the listing pass is
  returned to `testArchiveWithPasswordPrompt`, which asks for the password and
  retries the whole cycle, exactly as an error from the testing pass does.
- **Concurrency.** `SevenZip.Extract` calls its file handler from several
  goroutines at once, one per compression stream. The tested-byte counter is
  atomic and the failure list is now behind a mutex; the previous code appended
  to a plain slice from those goroutines.

## Three-pass review

1. Correctness: the denominator comes from the archive's own headers, the
   numerator from the member reads that actually happen, and both the coarse
   fallback and the final 100% update keep the shapes they had. An archive of
   only empty members reports 100% rather than hiding the bar, since such a
   pass is finished rather than of unknown length.
2. Concurrency: the counter is atomic, the failure list is locked, and the
   listing accumulator has its own mutex. No new goroutines are started.
3. Scope: the change is confined to the Shift-F3 testing path in
   `plugins/archive/archive.go`. Browsing, extraction and archive creation are
   untouched, and multi-volume handling is unchanged — it belongs to the
   separate ticket the issue asks for.

## Not covered here

Multi-volume archives, SFX or otherwise, and write operations on containers.
