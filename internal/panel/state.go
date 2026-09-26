package panel

// What the panels looked like when f4 last exited: where each side was, what
// it had under the cursor, how it sorted and whether it was shown. The root
// fills these from the session ini before the frame is built and reads them
// back when it writes one.

var (
	LastLeftPath         = ""
	LastRightPath        = ""
	LastLeftCursor       = ""
	LastRightCursor      = ""
	LastActivePanel      = 1
	LastWidePanel        = -1
	LastLeftViewMode     = 0
	LastRightViewMode    = 0
	LastLeftSortMode     = 0
	LastRightSortMode    = 0
	LastLeftSortRev      = false
	LastRightSortRev     = false
	LastLeftSortGroups   = false
	LastRightSortGroups  = false
	LastLeftSortNumeric  = false
	LastRightSortNumeric = false
	LastShowPanels       = true
	LastShowLeft         = true
	LastShowRight        = true
)

// AIPrevPath remembers where a panel was before it showed the dialog, so the
// same key brings the files back.
var AIPrevPath [2]string

var (
	LastLeftGroupBy, LastRightGroupBy           GroupMode
	LastLeftGroupReverse, LastRightGroupReverse bool
	LastLeftGroupFoldersSeparately              = true
	LastRightGroupFoldersSeparately             = true
)
