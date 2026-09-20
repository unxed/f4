package terminal

// ServerDiagnosticArgs are the profiling switches the daemon is started with.
// In the terminal the UI is drawn by the daemon, and the process the user ran
// only hands its terminal over, so a profile of that process holds nothing of
// what a user wants to measure (#884). Set by the command line parser; it is
// empty on platforms that have no daemon.
var ServerDiagnosticArgs []string
