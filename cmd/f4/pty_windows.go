//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/unxed/vtui"
	"golang.org/x/sys/windows"
)

var (
	modKernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procCreatePseudoConsole = modKernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole = modKernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole  = modKernel32.NewProc("ClosePseudoConsole")
)

// conPTYAvailable checks whether the Windows ConPTY API is available in kernel32.dll
// without panicking on older Windows versions (Windows 7, 8, 8.1, Server 2012/2016).
func conPTYAvailable() bool {
	if vtui.IsWine() {
		return false
	}
	return procCreatePseudoConsole.Find() == nil &&
		procResizePseudoConsole.Find() == nil &&
		procClosePseudoConsole.Find() == nil
}
func isPlatformPTYUsable() bool {
	return conPTYAvailable()
}

// PTY для Windows реализован через ConPTY API (доступно в Windows 10+).
type PTY struct {
	mu        sync.Mutex
	console   windows.Handle
	inPipe    windows.Handle
	outPipe   windows.Handle
	process   *windows.ProcessInformation
	inWriter  *os.File
	outReader *os.File

	lastBusyCheck time.Time
	lastBusyState bool

	// consoleClosed records that ClosePseudoConsole has run, whether from
	// Close or from the exit watcher, so the two never close it twice.
	consoleClosed bool

	Cmd *exec.Cmd //for interface compatability with Pty_unix, DO NOT USE
}

func NewPTY() (*PTY, error) {
	if !conPTYAvailable() {
		return nil, fmt.Errorf("ConPTY is not supported on this Windows version (requires Windows 10 build 1809+)")
	}

	var inPipeOur, inPipePty windows.Handle
	var outPipeOur, outPipePty windows.Handle

	// Создаем пайпы для ввода-вывода (CreatePipe: readHandle, writeHandle)
	// inPipe: PTY читает, мы пишем
	if err := windows.CreatePipe(&inPipePty, &inPipeOur, nil, 0); err != nil {
		return nil, err
	}
	// outPipe: мы читаем, PTY пишет
	if err := windows.CreatePipe(&outPipeOur, &outPipePty, nil, 0); err != nil {
		windows.CloseHandle(inPipePty)
		windows.CloseHandle(inPipeOur)
		return nil, err
	}

	// Создаем псевдоконсоль
	var console windows.Handle
	size := windows.Coord{X: 80, Y: 24}
	err := windows.CreatePseudoConsole(size, inPipePty, outPipePty, 0, &console)
	if err != nil {
		windows.CloseHandle(inPipePty)
		windows.CloseHandle(inPipeOur)
		windows.CloseHandle(outPipePty)
		windows.CloseHandle(outPipeOur)
		return nil, fmt.Errorf("failed to create pseudo console: %w (requires Windows 10+)", err)
	}

	// Закрываем наши копии хэндлов PTY, чтобы EOF корректно передавался при закрытии дочернего процесса
	windows.CloseHandle(inPipePty)
	windows.CloseHandle(outPipePty)

	return &PTY{
		console:   console,
		inPipe:    inPipeOur,
		outPipe:   outPipeOur,
		inWriter:  os.NewFile(uintptr(inPipeOur), "|in"),
		outReader: os.NewFile(uintptr(outPipeOur), "|out"),
	}, nil
}

func (p *PTY) Write(b []byte) (int, error) {
	vtui.DebugLog("PTY_WIN_TRACE: Writing %d bytes: %q", len(b), string(b))
	return p.inWriter.Write(b)
}

func (p *PTY) Read(b []byte) (int, error) {
	n, err := p.outReader.Read(b)
	if n > 0 {
		vtui.DebugLog("PTY_WIN_TRACE: Read %d bytes: %q", n, string(b[:n]))
	}
	return n, err
}

func (p *PTY) SetSize(cols, rows int) {
	// A minimized window reports 0x0; ConPTY does not survive being told so
	// (TERMINAL.md, rule 4). Nor does it need a height it cannot use.
	if cols <= 0 || rows <= 0 {
		vtui.DebugLog("PTY_WIN_SIZE: resize to %dx%d ignored (non-positive)", cols, rows)
		return
	}
	// COORD carries int16; anything above that cannot be what the host
	// window measures, and silently truncating it would hand the child a
	// different width than the one f4 lays its own screen out for (#907).
	if cols > 0x7FFF || rows > 0x7FFF {
		vtui.DebugLog("PTY_WIN_SIZE: resize to %dx%d ignored (exceeds COORD)", cols, rows)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.consoleClosed {
		vtui.DebugLog("PTY_WIN_SIZE: resize to %dx%d ignored (console closed)", cols, rows)
		return
	}
	// The child wraps its output at the width recorded here, so when its
	// lines break earlier than the window edge (#907) this is the line to
	// compare against REFLOW_PTY and FM_RESIZE. The HRESULT used to be
	// dropped on the floor; a refused resize left the pseudoconsole at its
	// previous size with nothing in the log to say so.
	err := windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
	if err != nil {
		vtui.DebugLog("PTY_WIN_SIZE: ResizePseudoConsole(%dx%d) failed: %v", cols, rows, err)
		return
	}
	vtui.DebugLog("PTY_WIN_SIZE: ResizePseudoConsole(%dx%d) ok", cols, rows)
}

func (p *PTY) Run(name string, args ...string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	cmdLine := windows.StringToUTF16Ptr(name)

	var attrList *windows.ProcThreadAttributeListContainer
	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}

	err = attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(p.console), unsafe.Sizeof(p.console))
	if err != nil {
		return err
	}

	si := &windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:    uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags: windows.STARTF_USESTDHANDLES,
		},
		ProcThreadAttributeList: attrList.List(),
	}

	pi := &windows.ProcessInformation{}
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	env := utf16.Encode([]rune(strings.Join(terminalChildEnv(), "\x00") + "\x00"))
	env = append(env, 0)

	err = windows.CreateProcess(nil, cmdLine, nil, nil, false, flags, &env[0], nil, &si.StartupInfo, pi)
	if err != nil {
		return err
	}

	p.process = pi
	p.watchExit(pi.Process)
	return nil
}

// watchExit closes the pseudoconsole once the shell process is gone, so that
// Read returns EOF the way a Unix pty master does when its shell exits.
//
// ConPTY does not do this by itself: conhost keeps the output pipe open after
// the client process has exited, until ClosePseudoConsole is called. Without
// the watcher, `exit` inside a batch file (which ends cmd.exe itself, unlike
// `exit /b`) left f4 reading a pipe that would never deliver another byte:
// no prompt could arrive, the panels stayed hidden behind a shell that no
// longer existed, and neither Ctrl+C nor Ctrl+Break had anyone to reach
// (issue #409).
//
// The watcher waits on its own duplicate of the process handle, so Close
// releasing the original cannot pull the handle out from under the wait.
func (p *PTY) watchExit(process windows.Handle) {
	var dup windows.Handle
	self := windows.CurrentProcess()
	if err := windows.DuplicateHandle(self, process, self, &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		vtui.DebugLog("PTY_WIN: cannot watch the shell process for exit: %v", err)
		return
	}
	go func() {
		defer windows.CloseHandle(dup)
		if _, err := windows.WaitForSingleObject(dup, windows.INFINITE); err != nil {
			vtui.DebugLog("PTY_WIN: waiting for the shell process failed: %v", err)
			return
		}
		vtui.DebugLog("PTY_WIN: shell process exited, closing the pseudoconsole")
		p.closeConsole()
	}()
}

// closeConsole runs ClosePseudoConsole once. The read loop must keep
// draining the output pipe meanwhile: ClosePseudoConsole flushes conhost's
// remaining output and does not return until it has been read.
func (p *PTY) closeConsole() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.consoleClosed {
		return
	}
	p.consoleClosed = true
	windows.ClosePseudoConsole(p.console)
}

func (p *PTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.process != nil {
		windows.TerminateProcess(p.process.Process, 0)
		// TerminateProcess only asks; the process is gone a little later.
		// Wait for it (bounded), so that whatever it held -- its working
		// directory above all -- is released by the time Close returns.
		windows.WaitForSingleObject(p.process.Process, 2000)
		windows.CloseHandle(p.process.Process)
		windows.CloseHandle(p.process.Thread)
		p.process = nil
	}
	if !p.consoleClosed {
		p.consoleClosed = true
		windows.ClosePseudoConsole(p.console)
	}
	p.inWriter.Close()
	p.outReader.Close()
	return nil
}

func (p *PTY) Wait() error {
	if p.process == nil {
		return nil
	}
	_, err := windows.WaitForSingleObject(p.process.Process, windows.INFINITE)
	return err
}

func (p *PTY) IsBusy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.process == nil {
		return false
	}

	// Increase cache timeout to 1 second to prevent high CPU usage
	// from CreateToolhelp32Snapshot during idle UI redraws.
	if time.Since(p.lastBusyCheck) < 1000*time.Millisecond {
		return p.lastBusyState
	}

	var exitCode uint32
	err := windows.GetExitCodeProcess(p.process.Process, &exitCode)
	if err != nil || exitCode != 259 { // 259 = STILL_ACTIVE
		p.lastBusyState = false
		p.lastBusyCheck = time.Now()
		return false
	}

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		p.lastBusyState = false
		p.lastBusyCheck = time.Now()
		return false
	}
	defer windows.CloseHandle(snapshot)

	var pe32 windows.ProcessEntry32
	pe32.Size = uint32(unsafe.Sizeof(pe32))

	if err := windows.Process32First(snapshot, &pe32); err != nil {
		p.lastBusyState = false
		p.lastBusyCheck = time.Now()
		return false
	}

	for {
		if pe32.ParentProcessID == p.process.ProcessId {
			if processIsGUI(pe32.ProcessID) {
				if err := windows.Process32Next(snapshot, &pe32); err != nil {
					break
				}
				continue
			}
			p.lastBusyState = true
			p.lastBusyCheck = time.Now()
			return true
		}
		if err := windows.Process32Next(snapshot, &pe32); err != nil {
			break
		}
	}

	p.lastBusyState = false
	p.lastBusyCheck = time.Now()
	return false
}

// ChildProcesses lists the shell's direct children with the one thing the
// session needs to know about each: whether cmd is waiting for it. A GUI
// child is not waited for. Unlike IsBusy this is not cached; it is only
// called while a prompt is being examined.
func (p *PTY) ChildProcesses() []childProcess {
	p.mu.Lock()
	proc := p.process
	p.mu.Unlock()
	if proc == nil {
		return nil
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)

	var pe32 windows.ProcessEntry32
	pe32.Size = uint32(unsafe.Sizeof(pe32))
	if err := windows.Process32First(snapshot, &pe32); err != nil {
		return nil
	}
	var children []childProcess
	for {
		if pe32.ParentProcessID == proc.ProcessId {
			name := windows.UTF16ToString(pe32.ExeFile[:])
			children = append(children, childProcess{Name: name, GUI: processIsGUI(pe32.ProcessID)})
		}
		if err := windows.Process32Next(snapshot, &pe32); err != nil {
			break
		}
	}
	return children
}

// processIsGUI reads the subsystem of the process's image. Anything that
// cannot be read (an elevated child, a vanished one) counts as a console
// program: waiting for it is the safe error.
func processIsGUI(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return false
	}
	gui, err := executableIsGUI(windows.UTF16ToString(buf[:size]))
	return err == nil && gui
}

func GetSystemShell() string {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		return "cmd.exe"
	}
	return shell
}
