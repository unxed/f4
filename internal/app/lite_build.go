//go:build !lite

package app

// liteBuild is false here and true in lite_build_lite.go (built only with
// -tags lite). shouldTryGui (bootstrap.go) is the one place that reads it: a
// lite build never auto-detects a display and tries a GUI backend, since
// internal/gui's lite build tag already makes every GUI backend fail to
// start (see internal/gui/run_lite.go) — checking here first just avoids a
// pointless attempt and a scarier error message on a headless router.
const liteBuild = false
