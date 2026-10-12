package vfs

type SudoCommand byte

const (
	CmdPing SudoCommand = iota
	CmdOpen
	CmdCreate
	CmdStat
	CmdReadDir
	CmdMkDir
	CmdRemove
	CmdRename
	CmdSetAttributes
	// CmdSymlink makes Path a symbolic link that points at Path2.
	CmdSymlink
	// CmdHardlink makes Path a hard link to the existing Path2.
	CmdHardlink
	// CmdUnmount unmounts the filesystem mounted at Path (f4#415). It is
	// the fallback UnmountDevice (mount_linux.go) reaches for only when
	// udisksctl is unavailable or refuses: unmounting something the current
	// user did not mount typically needs root, unlike the rest of this
	// dispatcher's calls, which exist for permission-denied filesystem
	// operations in general.
	CmdUnmount
	// CmdHello opens a session with an elevated dispatcher that cannot lean on
	// the file system to say who is calling (Windows, f4#1768): Path carries the
	// one-time token the dispatcher was started with. Nothing else is answered
	// before it has been accepted.
	CmdHello
	// CmdReadAt reads up to Size bytes of Path from Offset into Data of the
	// answer; IsEOF says the file ends there. The elevated dispatcher of
	// f4#1768 cannot pass a descriptor to its client on Windows, so a
	// file's contents travel by requests.
	CmdReadAt
	// CmdWriteAt writes Data into Path at Offset, creating the file with Mode
	// when it is missing; SudoWriteTruncate in Flags empties it first.
	CmdWriteAt
)

// SudoWriteTruncate, in Flags of a CmdWriteAt, empties the file before the
// write: the first chunk of a file written anew.
const SudoWriteTruncate = 1

// SudoWriteExclusive, in Flags of a CmdWriteAt, creates the file only when
// it does not exist yet (O_EXCL): the editor's staged sibling (f4#1768).
const SudoWriteExclusive = 2

// SudoChunkLimit is the most a CmdReadAt answers or a CmdWriteAt carries,
// well inside sudoFrameLimit once base64 has grown it by a third.
const SudoChunkLimit = 4 << 20

// SudoRenameNoReplace, in Flags of a CmdRename, makes the dispatcher refuse to
// replace what is already at the destination, atomically, as a plain
// RenameNoReplace does.
const SudoRenameNoReplace = 1

type SudoRequest struct {
	Cmd   SudoCommand
	Path  string
	Path2 string  // Used for rename
	Flags int     // OS flags (e.g. O_RDONLY)
	Mode  uint32  // File permissions
	Item  VFSItem // Used for SetAttributes
	// Offset, Size and Data are the part of a file CmdReadAt reads and
	// CmdWriteAt writes.
	Offset int64  `json:",omitempty"`
	Size   int    `json:",omitempty"`
	Data   []byte `json:",omitempty"`
}

type SudoResponse struct {
	Error string
	Item  VFSItem
	Items []VFSItem
	IsEOF bool
	// Data is what a CmdReadAt read.
	Data []byte `json:",omitempty"`
	// Errno names the kind of the error -- "ENOENT", "EACCES", ... -- the
	// same on every platform, while Error is the dispatcher's own text: on
	// Windows that text is not the one the client could recognise (f4#1768).
	Errno string `json:",omitempty"`
}
