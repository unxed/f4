//go:build windows

// Command stub stands in for f4.exe in the launcher's test: it writes its
// arguments and whether it has a console to the file F4_STUB_OUT.
package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func main() {
	out := os.Getenv("F4_STUB_OUT")
	if out == "" {
		os.Exit(3)
	}
	getConsole := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	hwnd, _, _ := getConsole.Call()
	text := fmt.Sprintf("args=%s\nconsole=%d\n", strings.Join(os.Args[1:], "|"), map[bool]int{true: 1, false: 0}[hwnd != 0])
	if err := os.WriteFile(out, []byte(text), 0o600); err != nil {
		os.Exit(4)
	}
}
