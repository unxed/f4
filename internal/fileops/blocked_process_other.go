//go:build !windows

package fileops

// ProcessesUsingFile reports processes the OS identifies as using path.
func ProcessesUsingFile(path string) ([]FileProcess, error) {
	return nil, ErrProcessLookupUnsupported
}
