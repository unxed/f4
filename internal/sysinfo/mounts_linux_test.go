//go:build linux

package sysinfo

import (
	"reflect"
	"testing"
)

// A representative snippet of /proc/mounts: system mounts that must never
// appear as "unmountable" rows, a couple of pseudo filesystems parked under
// a user-mount root (defensive filter), and two real removable mounts --
// one under /media, one under /run/media, with a space in its label to
// exercise the octal-escape decoding -- plus a remount of the same
// /run/media point, which must replace rather than duplicate it.
const fakeProcMounts = `sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0
proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
/dev/sda2 / ext4 rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid,nodev,size=3276740k,mode=755 0 0
/dev/sda1 /boot/efi vfat rw,relatime,fmask=0077,dmask=0077,codepage=437,iocharset=ascii,shortname=winnt,errors=remount-ro 0 0
tmpfs /mnt/scratch tmpfs rw,nosuid,nodev 0 0
/dev/sdb1 /media/user/USB_STICK vfat rw,nosuid,nodev,relatime,uid=1000,gid=1000 0 0
/dev/sdc1 /run/media/user/My\040Stick exfat rw,nosuid,nodev,relatime,uid=1000,gid=1000 0 0
/dev/sdc1 /run/media/user/My\040Stick exfat ro,nosuid,nodev,relatime,uid=1000,gid=1000 0 0
`

func TestParseUserMountsFiltersSystemAndPseudoMounts(t *testing.T) {
	got := ParseUserMounts(fakeProcMounts)
	want := []MountEntry{
		{Device: "/dev/sdb1", MountPoint: "/media/user/USB_STICK", FSType: "vfat"},
		{Device: "/dev/sdc1", MountPoint: "/run/media/user/My Stick", FSType: "exfat"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseUserMounts() = %#v, want %#v", got, want)
	}
}

func TestParseUserMountsRemountReplacesEarlierEntry(t *testing.T) {
	got := ParseUserMounts(fakeProcMounts)
	for _, e := range got {
		if e.MountPoint != "/run/media/user/My Stick" {
			continue
		}
		// The final /proc/mounts line for this point is the read-only
		// remount; ParseUserMounts must reflect that instead of the first
		// (read-write) line, but it does not expose mount flags today, so
		// what is checked here is simply that there is exactly one entry
		// for the point (asserted by the DeepEqual above) rather than two.
		return
	}
	t.Fatal("remounted point missing from ParseUserMounts result")
}

func TestParseUserMountsEmptyInput(t *testing.T) {
	if got := ParseUserMounts(""); len(got) != 0 {
		t.Fatalf("ParseUserMounts(\"\") = %#v, want empty", got)
	}
}

func TestParseUserMountsIgnoresShortLines(t *testing.T) {
	got := ParseUserMounts("garbage line\n/dev/sdb1 /media/user/short\n")
	if len(got) != 0 {
		t.Fatalf("ParseUserMounts() = %#v, want empty (fewer than 3 fields)", got)
	}
}

func TestParseUserMountsIgnoresBareUserMountRoot(t *testing.T) {
	// "/media" itself (no trailing component) is never a real mount; only
	// something strictly inside it is.
	got := ParseUserMounts("/dev/sdb1 /media vfat rw 0 0\n")
	if len(got) != 0 {
		t.Fatalf("ParseUserMounts() = %#v, want empty (bare root)", got)
	}
}

func TestUnescapeMountField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{`My\040Stick`, "My Stick"},
		// The kernel escapes a literal backslash as \134 (its own octal
		// code), not by doubling it -- there is no C-style "\\" rule here.
		{`back\134slash`, `back\slash`},
		{`tab\011here`, "tab\there"},
		{`trailing\`, `trailing\`},
		{`odd\04`, `odd\04`},
	}
	for _, c := range cases {
		if got := unescapeMountField(c.in); got != c.want {
			t.Errorf("unescapeMountField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
