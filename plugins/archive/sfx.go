package archive

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/unxed/zipper/archive"
)

const sfxProbeLimit = 64 << 20

type sfxSignature struct {
	magic  []byte
	format string
	suffix string
	// accept, when set, confirms that the magic at offset really starts an
	// archive. A stub can hold the same bytes as data of its own, and taking
	// such a match makes the archive unreadable.
	accept func(r io.ReaderAt, offset int64) (bool, error)
}

var sfxSignatures = []sfxSignature{
	{magic: []byte("PK\x03\x04"), format: "zip", suffix: ".zip"},
	{magic: []byte("PK\x05\x06"), format: "zip", suffix: ".zip"},
	{magic: []byte("7z\xBC\xAF\x27\x1C"), format: "fallback", suffix: ".7z", accept: sevenZipStartHeaderValid},
	{magic: []byte("Rar!\x1A\x07\x00"), format: "fallback", suffix: ".rar"},
	{magic: []byte("Rar!\x1A\x07\x01\x00"), format: "fallback", suffix: ".rar"},
}

// sevenZipStartHeaderSize is the fixed 7z start header: the 6-byte signature,
// 2 version bytes, the start header CRC, and the 20 bytes that CRC covers.
const sevenZipStartHeaderSize = 32

// sevenZipStartHeaderValid applies the test 7-Zip itself uses to accept a
// signature it finds while searching a file (TestStartCrc in
// CPP/7zip/Archive/7z/7zIn.cpp): the CRC32 stored at bytes 8..11 must match
// bytes 12..31. The official 7-Zip installer carries the signature inside its
// stub several kilobytes before the real archive; only the real one passes.
func sevenZipStartHeaderValid(r io.ReaderAt, offset int64) (bool, error) {
	var header [sevenZipStartHeaderSize]byte
	if _, err := r.ReadAt(header[:], offset); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return crc32.ChecksumIEEE(header[12:]) == binary.LittleEndian.Uint32(header[8:12]), nil
}

// nextSFXCandidate returns the earliest signature match in block at or after
// from, or -1 when there is none.
func nextSFXCandidate(block []byte, from int) (int, sfxSignature) {
	bestIndex := -1
	var best sfxSignature
	for _, signature := range sfxSignatures {
		index := bytes.Index(block[from:], signature.magic)
		if index < 0 {
			continue
		}
		index += from
		if bestIndex < 0 || index < bestIndex {
			bestIndex = index
			best = signature
		}
	}
	return bestIndex, best
}

type embeddedArchive struct {
	format string
	suffix string
	offset int64
}

func findEmbeddedArchive(filename string) (embeddedArchive, bool, error) {
	file, err := os.Open(filename)
	if err != nil {
		return embeddedArchive{}, false, err
	}
	defer func() { _ = file.Close() }()
	return scanEmbeddedArchive(file, file, sfxProbeLimit)
}

// scanEmbeddedArchive looks for the first archive signature in the first
// limit bytes of source. reader is read sequentially from its current
// position; at is the same file seen as random access, which the signatures
// that validate themselves need and which must therefore address the same
// bytes from offset zero.
func scanEmbeddedArchive(reader io.Reader, at io.ReaderAt, limit int64) (embeddedArchive, bool, error) {
	const chunkSize = 64 << 10
	const maxNoProgressReads = 100
	maxMagic := 0
	for _, signature := range sfxSignatures {
		if len(signature.magic) > maxMagic {
			maxMagic = len(signature.magic)
		}
	}

	chunk := make([]byte, chunkSize)
	var carry []byte
	var scanned int64
	var noProgress int
	for scanned < limit {
		want := int64(len(chunk))
		if remaining := limit - scanned; remaining < want {
			want = remaining
		}
		n, readErr := reader.Read(chunk[:int(want)])
		if n == 0 && readErr == nil {
			// A reader is discouraged from returning nothing and no error,
			// but it is allowed to, and the loop only advances on bytes. Give
			// up the way io.ReadAtLeast does rather than spin on a reader
			// that has stopped making progress.
			if noProgress++; noProgress >= maxNoProgressReads {
				return embeddedArchive{}, false, io.ErrNoProgress
			}
			continue
		}
		noProgress = 0
		if n > 0 {
			block := make([]byte, 0, len(carry)+n)
			block = append(block, carry...)
			block = append(block, chunk[:n]...)
			blockStart := scanned - int64(len(carry))

			for from := 0; from < len(block); {
				index, candidate := nextSFXCandidate(block, from)
				if index < 0 {
					break
				}
				offset := blockStart + int64(index)
				accepted := true
				if candidate.accept != nil {
					var acceptErr error
					if accepted, acceptErr = candidate.accept(at, offset); acceptErr != nil {
						return embeddedArchive{}, false, acceptErr
					}
				}
				if accepted {
					return embeddedArchive{
						format: candidate.format,
						suffix: candidate.suffix,
						offset: offset,
					}, true, nil
				}
				// Rejected: keep searching past it rather than giving up on
				// the file, because the real archive usually follows.
				from = index + 1
			}

			keep := maxMagic - 1
			if len(block) > keep {
				carry = append(carry[:0], block[len(block)-keep:]...)
			} else {
				carry = append(carry[:0], block...)
			}
			scanned += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return embeddedArchive{}, false, readErr
		}
	}

	return embeddedArchive{}, false, nil
}

type sfxBacking struct {
	path string
	dir  string
	once sync.Once
	err  error
}

func (b *sfxBacking) Close() error {
	b.once.Do(func() {
		if b.dir != "" {
			b.err = os.RemoveAll(b.dir)
		} else {
			b.err = os.Remove(b.path)
			if errors.Is(b.err, os.ErrNotExist) {
				b.err = nil
			}
		}
	})
	return b.err
}

type sfxVolume struct {
	source string
	target string
}

type sfxVolumePlan struct {
	first      string
	companions []sfxVolume
}

func sfxVolumePlanFor(filename string, embedded embeddedArchive) (sfxVolumePlan, error) {
	base := filepath.Base(filename)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	plan := sfxVolumePlan{first: stem + embedded.suffix}

	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil {
		return sfxVolumePlan{}, err
	}

	switch strings.ToLower(embedded.suffix) {
	case ".zip":
		for volume := 1; ; volume++ {
			target := fmt.Sprintf("%s.z%02d", stem, volume)
			source := findCaseInsensitiveEntry(entries, target)
			if source == "" {
				break
			}
			plan.companions = append(plan.companions, sfxVolume{
				source: filepath.Join(filepath.Dir(filename), source),
				target: target,
			})
		}
	case ".7z":
		for volume := 2; ; volume++ {
			target := fmt.Sprintf("%s.7z.%03d", stem, volume)
			source := findCaseInsensitiveEntry(entries, target)
			if source == "" {
				break
			}
			plan.companions = append(plan.companions, sfxVolume{
				source: filepath.Join(filepath.Dir(filename), source),
				target: target,
			})
		}
		if len(plan.companions) > 0 {
			plan.first = stem + ".7z.001"
		}
	case ".rar":
		plan = planRARVolumes(filepath.Dir(filename), stem, entries)
	}

	return plan, nil
}

func findCaseInsensitiveEntry(entries []os.DirEntry, target string) string {
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), target) {
			return entry.Name()
		}
	}
	return ""
}

type rarVolumeMatch struct {
	name   string
	part   int
	width  int
	total  string
	legacy bool
}

func planRARVolumes(dir, stem string, entries []os.DirEntry) sfxVolumePlan {
	newPattern := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(stem) + `\.part([0-9]+)(?:of([0-9]+))?\.rar$`)
	legacyPattern := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(stem) + `\.r([0-9]+)$`)
	var modern []rarVolumeMatch
	var legacy []rarVolumeMatch
	for _, entry := range entries {
		name := entry.Name()
		if match := newPattern.FindStringSubmatch(name); match != nil {
			part, err := strconv.Atoi(match[1])
			if err == nil && part >= 2 {
				modern = append(modern, rarVolumeMatch{
					name:  name,
					part:  part,
					width: len(match[1]),
					total: match[2],
				})
			}
			continue
		}
		if match := legacyPattern.FindStringSubmatch(name); match != nil {
			part, err := strconv.Atoi(match[1])
			if err == nil {
				legacy = append(legacy, rarVolumeMatch{
					name:   name,
					part:   part,
					width:  len(match[1]),
					legacy: true,
				})
			}
		}
	}

	plan := sfxVolumePlan{first: stem + ".rar"}
	if len(modern) > 0 {
		sort.Slice(modern, func(i, j int) bool { return modern[i].part < modern[j].part })
		first := modern[0]
		firstPart := fmt.Sprintf("%0*d", first.width, 1)
		plan.first = stem + ".part" + firstPart
		if first.total != "" {
			plan.first += "of" + first.total
		}
		plan.first += ".rar"
		for _, volume := range modern {
			part := fmt.Sprintf("%0*d", first.width, volume.part)
			target := stem + ".part" + part
			if first.total != "" {
				target += "of" + first.total
			}
			plan.companions = append(plan.companions, sfxVolume{
				source: filepath.Join(dir, volume.name),
				target: target + ".rar",
			})
		}
		return plan
	}

	if len(legacy) > 0 {
		sort.Slice(legacy, func(i, j int) bool { return legacy[i].part < legacy[j].part })
		first := legacy[0]
		for _, volume := range legacy {
			target := stem + ".r" + fmt.Sprintf("%0*d", first.width, volume.part)
			plan.companions = append(plan.companions, sfxVolume{
				source: filepath.Join(dir, volume.name),
				target: target,
			})
		}
	}
	return plan
}

func copySFXFile(dst, source string, offset int64) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()

	output, err := os.Create(dst)
	if err != nil {
		return err
	}
	removeOutput := true
	defer func() {
		_ = output.Close()
		if removeOutput {
			_ = os.Remove(dst)
		}
	}()

	if offset > 0 {
		stat, err := input.Stat()
		if err != nil {
			return err
		}
		if offset >= stat.Size() {
			return os.ErrInvalid
		}
		if _, err := io.Copy(output, io.NewSectionReader(input, offset, stat.Size()-offset)); err != nil {
			return err
		}
	} else if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	removeOutput = false
	return nil
}

// materializeLocalSFX probes a local file for an archive embedded after an
// executable stub and, when one is found, copies it to a private backing file
// the archive readers can open. The returned embeddedArchive has a zero offset
// when there is nothing to materialize; the path is then localPath itself.
//
// materializeLocalSFX is the entry point for a file that is where its author
// put it, so the rest of a split archive can be lying beside it and is
// collected along with it.
func materializeLocalSFX(localPath string) (embeddedArchive, string, io.Closer, error) {
	return materializeSFX(localPath, true)
}

// materializeNestedSFX probes a copy that was materialized out of another
// file system into a temporary directory of its own. It is the same probe,
// minus the search for companion volumes: only this one member was
// materialized, so its split siblings cannot be next to it, and looking for
// them there means reading the whole system temporary directory on every open
// -- and failing the open when that read fails.
func materializeNestedSFX(localPath string) (embeddedArchive, string, io.Closer, error) {
	return materializeSFX(localPath, false)
}

// zipEndRecordAtTail reports whether the file ends with a zip end-of-central-
// directory record: the signature within the last 64 KB, which is where the
// format puts it (the record is 22 bytes and its comment up to 65535).
func zipEndRecordAtTail(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return true // cannot tell; the open that follows reports the real error
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return true
	}
	const record, maxComment = 22, 65535
	size := int64(record + maxComment)
	if info.Size() < size {
		size = info.Size()
	}
	tail := make([]byte, size)
	if _, err := file.ReadAt(tail, info.Size()-size); err != nil && !errors.Is(err, io.EOF) {
		return true
	}
	for i := len(tail) - record; i >= 0; i-- {
		if !bytes.HasPrefix(tail[i:], []byte("PK\x05\x06")) {
			continue
		}
		if i+record+int(binary.LittleEndian.Uint16(tail[i+20:i+22])) <= len(tail) {
			return true
		}
	}
	return false
}

func materializeSFX(localPath string, volumesBeside bool) (embeddedArchive, string, io.Closer, error) {
	// A zip keeps the offsets of its entries in the central directory at the
	// end of the file, and a tool that appends one to an executable stub may
	// count the stub in those offsets or not. The zip reader works out which
	// it is and reads the archive where it lies, so such a file is handed
	// over whole; copying the archive out from under the stub invalidates
	// every offset that counted the stub in, and the entries are then read
	// by guesswork, without the parameters their headers carry -- for a
	// WinRAR self-extracting archive with AES that means "zip: AES info
	// missing" for every member, once the password has been given
	// (issue #1186).
	if archive.DetectFormat(localPath) == "zip" {
		return embeddedArchive{format: "zip"}, localPath, nil, nil
	}

	embedded, found, err := findEmbeddedArchive(localPath)
	if err != nil {
		return embeddedArchive{}, "", nil, err
	}
	if !found || embedded.offset <= 0 {
		return embeddedArchive{}, localPath, nil, nil
	}
	// The scan takes the first "PK" header it sees, and an executable can hold
	// those bytes as data of its own (an installer's compressed payload does).
	// The zip reader then hunts through the rest of the file for entries to
	// recover, which took half a minute on a 30 MB installer and minutes on a
	// larger program (#1272). A zip that is really there has its end record in
	// the last 64 KB; without one the file is not treated as an archive.
	if embedded.suffix == ".zip" && !zipEndRecordAtTail(localPath) {
		return embeddedArchive{}, localPath, nil, nil
	}
	var backingPath string
	var closer io.Closer
	if volumesBeside {
		backingPath, closer, err = materializeEmbeddedArchive(localPath, embedded)
	} else {
		backingPath, closer, err = materializeEmbeddedArchiveAlone(localPath, embedded)
	}
	if err != nil {
		return embeddedArchive{}, "", nil, err
	}
	return embedded, backingPath, closer, nil
}

// localArchiveBacking returns the file the archive readers should be given for
// a local archive path: the path itself, or for a self-extracting archive the
// same private copy panel entry reads (see NewArchiveVFSContext). Testing and
// extracting must go through it, or an SFX that opens in the panel fails both
// with "no formats matched". A non-nil closer removes the copy.
func localArchiveBacking(srcPath string) (string, io.Closer, error) {
	if archive.DetectFormat(filepath.Base(srcPath)) != "" {
		return srcPath, nil, nil
	}
	_, backingPath, closer, err := materializeLocalSFX(srcPath)
	return backingPath, closer, err
}

func materializeEmbeddedArchive(filename string, embedded embeddedArchive) (string, io.Closer, error) {
	if embedded.offset <= 0 {
		return filename, nil, nil
	}

	plan, err := sfxVolumePlanFor(filename, embedded)
	if err != nil {
		return "", nil, err
	}
	if len(plan.companions) == 0 {
		return materializeEmbeddedArchiveAlone(filename, embedded)
	}

	dir, err := os.MkdirTemp("", "f4-sfx-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	targetName := filepath.Join(dir, plan.first)
	if err := copySFXFile(targetName, filename, embedded.offset); err != nil {
		cleanup()
		return "", nil, err
	}
	for _, volume := range plan.companions {
		if err := copySFXFile(filepath.Join(dir, volume.target), volume.source, 0); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return targetName, &sfxBacking{path: targetName, dir: dir}, nil
}

// materializeEmbeddedArchiveAlone copies the archive out from under its stub
// and nothing else. It is what a single self-extracting file needs, whether
// it never had companion volumes or is a copy that was materialized without
// them.
func materializeEmbeddedArchiveAlone(filename string, embedded embeddedArchive) (string, io.Closer, error) {
	if embedded.offset <= 0 {
		return filename, nil, nil
	}
	target, err := os.CreateTemp("", "f4-sfx-*"+embedded.suffix)
	if err != nil {
		return "", nil, err
	}
	targetName := target.Name()
	if err := target.Close(); err != nil {
		_ = os.Remove(targetName)
		return "", nil, err
	}
	if err := copySFXFile(targetName, filename, embedded.offset); err != nil {
		_ = os.Remove(targetName)
		return "", nil, err
	}
	return targetName, &sfxBacking{path: targetName}, nil
}
