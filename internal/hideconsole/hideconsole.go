package hideconsole

// Hide is a dummy implementation to prevent Ebitengine from destroying the
// Windows console when launched independently (e.g. via Shift+Enter in Far).
func Hide() error {
	return nil
}
