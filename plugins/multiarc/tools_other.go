//go:build !windows

package multiarc

import "context"

// platformToolArgs returns args unchanged: only Windows paths need a tool
// told how to read them (tools_windows.go).
func platformToolArgs(_ context.Context, _ string, args []string) []string {
	return args
}
