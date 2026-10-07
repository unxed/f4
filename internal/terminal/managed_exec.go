package terminal

import "fmt"

// ManagedForegroundCommand wraps sqCmd -- a command already shell-quoted for
// eval, e.g. via ShellSingleQuote -- in the OSC 133 C/D markers f4 needs in
// order to know when a foreground command run from its own command line has
// finished. See internal/panel/frame.go's composition of fullWireCmd for the
// caller (the "managed foreground command" branch), and
// managed_exec_test.go for what a job-control stop (Ctrl+Z / SIGTSTP) does
// to the D marker printed here -- it never runs, which is the mechanism
// behind f4 #1603's stuck-forever terminal.
func ManagedForegroundCommand(sqCmd string) string {
	return fmt.Sprintf("{ trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; eval %s ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }", sqCmd)
}

// ManagedForegroundCommandInDirectory runs the managed command only after a
// directory change succeeded. A plain `cd dir && ManagedForegroundCommand`
// is not sufficient: when the directory needs sudo, the unprivileged shell
// rejects cd and short-circuits the OSC wrapper, leaving f4 waiting forever
// for its completion marker (f4#1255).
//
// sqPath and sqCmd are already shell-quoted for the POSIX shell. The failure
// arm emits the same C/D markers as a managed command, then preserves cd's
// exit status, so the terminal returns to the panels instead of hanging.
func ManagedForegroundCommandInDirectory(sqPath, sqCmd string) string {
	return fmt.Sprintf("set +H; if cd %s; then %s; else FARVTRESULT=$?; printf \"\\033]133;C\\007\\033]133;D\\007\"; (exit $FARVTRESULT); fi", sqPath, ManagedForegroundCommand(sqCmd))
}
