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
)

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
}

type SudoResponse struct {
	Error string
	Item  VFSItem
	Items []VFSItem
	IsEOF bool
}
