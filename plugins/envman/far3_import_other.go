//go:build !windows

package envman

func far3ImportSupported() bool {
	return false
}

func loadFar3ImportCandidates() ([]far3ImportCandidate, error) {
	return nil, errFar3ImportUnsupported
}
