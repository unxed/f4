//go:build linux

package sysinfo

import (
	"os"
	"strconv"
	"strings"
)

// MountEntry is one line of /proc/mounts that looks like a mount a desktop
// user (or an admin who typed `mount` by hand) would actually want to see,
// and possibly unmount, from f4's disk menu -- a removable flash drive, not
// "/", "/proc" or a bind mount the init system created (f4#415).
type MountEntry struct {
	// Device is the first /proc/mounts field: the block device backing the
	// mount (e.g. "/dev/sdb1"), used both as the udisksctl -b argument and
	// as the disk-menu row's disambiguating detail.
	Device string
	// MountPoint is the second /proc/mounts field, unescaped.
	MountPoint string
	// FSType is the third /proc/mounts field (e.g. "vfat", "ext4").
	FSType string
}

// userMountRoots are where udisks2 (the mechanism behind GNOME's, KDE's and
// most other desktops' automount-on-plug-in) and a manual admin mount both
// put removable media. Anything outside these roots is a system mount --
// "/", "/boot", "/home", a container overlay under "/var/lib/docker", a
// snap's "/run/snapd" -- that this menu has no business offering to
// unmount.
var userMountRoots = []string{"/media/", "/run/media/", "/mnt/"}

// pseudoFSTypes is a defensive second filter: even a filesystem mounted
// somewhere under one of userMountRoots (a container bind-mounting tmpfs
// under /mnt, say) is not a device to offer for unmounting.
var pseudoFSTypes = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true,
	"devpts": true, "cgroup": true, "cgroup2": true, "overlay": true,
	"squashfs": true, "autofs": true, "binfmt_misc": true, "bpf": true,
	"debugfs": true, "tracefs": true, "securityfs": true, "pstore": true,
	"hugetlbfs": true, "mqueue": true, "fusectl": true, "configfs": true,
	"ramfs": true, "rpc_pipefs": true, "nsfs": true, "efivarfs": true,
}

// underUserMountRoot reports whether mountPoint sits strictly inside one of
// userMountRoots (the root itself, e.g. bare "/media", never appears as a
// mount and is not one).
func underUserMountRoot(mountPoint string) bool {
	for _, root := range userMountRoots {
		if strings.HasPrefix(mountPoint, root) && len(mountPoint) > len(root) {
			return true
		}
	}
	return false
}

// unescapeMountField reverses the octal escaping /proc/mounts (like fstab)
// uses for space, tab, newline and backslash in a field, so a flash drive
// labeled "My Stick" is reported as "/media/user/My Stick", not
// "/media/user/My\040Stick".
func unescapeMountField(field string) string {
	if !strings.Contains(field, "\\") {
		return field
	}
	var b strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] == '\\' && i+3 < len(field) {
			if code, err := strconv.ParseUint(field[i+1:i+4], 8, 8); err == nil {
				// #nosec G115 -- ParseUint with bitSize 8 bounds code to a byte.
				b.WriteByte(byte(code))
				i += 3
				continue
			}
		}
		b.WriteByte(field[i])
	}
	return b.String()
}

// ParseUserMounts scans /proc/mounts-formatted data and returns the entries
// a desktop user could plausibly want in the disk menu: removable media
// mounted under /media, /run/media or /mnt, skipping pseudo filesystems even
// there. When the same mount point appears more than once (the kernel lists
// every mount event, and a remount or a stacked bind mount can add a later
// line for a point already seen), the last line for it wins, matching what
// is actually mounted there now.
func ParseUserMounts(procMounts string) []MountEntry {
	var entries []MountEntry
	indexByMountPoint := make(map[string]int)
	for _, line := range strings.Split(procMounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		device := unescapeMountField(fields[0])
		mountPoint := unescapeMountField(fields[1])
		fsType := fields[2]

		if !underUserMountRoot(mountPoint) {
			continue
		}
		if pseudoFSTypes[fsType] {
			continue
		}
		if device == "" || device == "none" {
			continue
		}

		entry := MountEntry{Device: device, MountPoint: mountPoint, FSType: fsType}
		if idx, ok := indexByMountPoint[mountPoint]; ok {
			entries[idx] = entry
		} else {
			indexByMountPoint[mountPoint] = len(entries)
			entries = append(entries, entry)
		}
	}
	return entries
}

// UserMounts reads /proc/mounts and returns the current removable-media
// mounts a user could reasonably want in the disk menu (see
// ParseUserMounts). A read failure (no /proc, e.g. a container without it
// bind-mounted) yields an empty list rather than an error: no removable
// mount rows are shown, exactly as if none were present.
func UserMounts() []MountEntry {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	return ParseUserMounts(string(data))
}
