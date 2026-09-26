package vfs

// MetadataFields records which optional values a provider actually obtained.
// MetadataExplicit makes missing bits authoritative, including for synthetic
// fallback timestamps. Without it, old providers retain conservative inference.
type MetadataFields uint32

const (
	MetadataPhysicalSize MetadataFields = 1 << iota
	MetadataPermissions
	MetadataUID
	MetadataGID
	MetadataWinAttrs
	MetadataHidden
	MetadataExecutable
	MetadataMTime
	MetadataATime
	MetadataCTime
	MetadataNlink
	// MetadataBTime marks VFSItem.BTime as a genuine, platform-reported
	// creation/birth time (see its doc comment for which platforms populate
	// it). Left unset, rather than inferred from a non-zero BTime, because a
	// bug that leaves BTime at its zero value would otherwise silently
	// "know" the object was created at the Unix epoch.
	MetadataBTime
	MetadataExplicit MetadataFields = 1 << 31
)

// HasMetadata distinguishes known zero values from omitted legacy fields.
func (item VFSItem) HasMetadata(field MetadataFields) bool {
	if item.KnownMetadata&field != 0 {
		return true
	}
	if item.KnownMetadata&MetadataExplicit != 0 {
		return false
	}
	switch field {
	case MetadataPhysicalSize:
		return item.PhysicalSize > 0
	case MetadataPermissions:
		return item.UnixMode != 0
	case MetadataUID:
		return item.Uid > 0
	case MetadataGID:
		return item.Gid > 0
	case MetadataWinAttrs:
		return item.WinAttrs != 0
	case MetadataHidden:
		return item.IsHidden
	case MetadataExecutable:
		return item.IsExecutable
	case MetadataMTime:
		return !item.MTime.IsZero()
	case MetadataATime:
		return !item.ATime.IsZero()
	case MetadataCTime:
		return !item.CTime.IsZero()
	case MetadataNlink:
		return item.Nlink > 0
	}
	return false
}
