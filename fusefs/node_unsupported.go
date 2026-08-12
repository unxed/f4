//go:build !linux && !darwin && !freebsd

package fusefs

import "context"

const supported = false

// startServer never succeeds here. The rest of the package still compiles,
// so callers can ask Supported() instead of guarding every reference with
// build tags of their own.
func startServer(ctx context.Context, m *Mount, opts Options) error {
	return ErrUnsupported
}
