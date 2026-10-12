package vfs

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"
)

// A directory junction is a reparse point with the mount point tag whose data
// names the directory it stands for. The layout is Microsoft's
// REPARSE_DATA_BUFFER with a MountPointReparseBuffer: the tag, the length of
// what follows, two reserved bytes, the offsets and lengths of the
// substitute name and of the print name, and then both names as UTF-16 each
// followed by a NUL (f4#1828). The buffer is built here, without Windows
// calls, so that it can be checked on every platform.

const (
	reparseMountPointHeader = 8 // the four 16-bit fields before the path buffer
	reparseGenericHeader    = 8 // tag, data length, reserved
)

// mountPointReparseBuffer returns the data FSCTL_SET_REPARSE_POINT wants to
// turn an empty directory into a junction to target, which must be an
// absolute path on a local drive ("C:\dir").
func mountPointReparseBuffer(target string) ([]byte, error) {
	target = strings.TrimPrefix(target, `\\?\`)
	if len(target) < 3 || target[1] != ':' || (target[2] != '\\' && target[2] != '/') {
		return nil, errors.New("a junction can only point to a folder on a local drive, like C:\\folder")
	}
	target = strings.ReplaceAll(target, "/", `\`)
	target = strings.TrimRight(target, `\`)
	if len(target) == 2 { // "C:" became the drive's root again
		target += `\`
	}
	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	pathBytes := (len(substitute) + 1 + len(printName) + 1) * 2
	dataLength := reparseMountPointHeader + pathBytes
	if dataLength > 16*1024-reparseGenericHeader {
		return nil, errors.New("junction target path is too long")
	}
	buf := make([]byte, reparseGenericHeader+dataLength)
	binary.LittleEndian.PutUint32(buf[0:], 0xA0000003) // IO_REPARSE_TAG_MOUNT_POINT
	binary.LittleEndian.PutUint16(buf[4:], uint16(dataLength))
	// buf[6:8] reserved.
	substituteBytes := uint16(len(substitute) * 2)             // #nosec G115 -- the length check above keeps both names far below 64 KiB
	printBytes := uint16(len(printName) * 2)                   // #nosec G115 -- the same
	binary.LittleEndian.PutUint16(buf[8:], 0)                  // substitute name offset
	binary.LittleEndian.PutUint16(buf[10:], substituteBytes)   // substitute name length, without the NUL
	binary.LittleEndian.PutUint16(buf[12:], substituteBytes+2) // print name offset
	binary.LittleEndian.PutUint16(buf[14:], printBytes)        // print name length
	offset := reparseGenericHeader + reparseMountPointHeader
	for _, u := range substitute {
		binary.LittleEndian.PutUint16(buf[offset:], u)
		offset += 2
	}
	offset += 2 // NUL
	for _, u := range printName {
		binary.LittleEndian.PutUint16(buf[offset:], u)
		offset += 2
	}
	return buf, nil
}
