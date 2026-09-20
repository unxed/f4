//go:build !windows

package vfs

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/unxed/vtui"
)

// RunSudoDispatcher starts the elevated backend server loop.
// It is spawned by SudoClient via `sudo -A`.
func RunSudoDispatcher(sockPath string) {
	fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: STARTING (EUID=%d, PID=%d)\n", os.Geteuid(), os.Getpid())
	// Use a dedicated log file because stderr might be unreliable under sudo
	sudoUid := os.Getenv("SUDO_UID")
	if sudoUid == "" {
		sudoUid = fmt.Sprintf("%d", os.Getuid())
	}
	sudoGid := os.Getenv("SUDO_GID")
	if sudoGid == "" {
		sudoGid = fmt.Sprintf("%d", os.Getgid())
	}
	invokingUID, uidErr := strconv.Atoi(sudoUid)
	invokingGID, gidErr := strconv.Atoi(sudoGid)
	if uidErr != nil || gidErr != nil || invokingUID < 0 || invokingGID < 0 {
		fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: Invalid SUDO_UID/SUDO_GID\n")
		os.Exit(1)
	}
	// Derive the filename from the validated integer, not the raw sudo
	// environment value: this process is root and a forged SUDO_UID must not
	// turn the diagnostic path into a traversal outside the temp directory.
	debugLogPath := filepath.Join(os.TempDir(), fmt.Sprintf("f4-sudo-debug-%d.txt", invokingUID))
	// O_NOFOLLOW: the path is predictable and lives in a directory every local
	// user can write, so without it another user can pre-create a symlink here
	// and have root append its diagnostics to a file of their choosing.
	debugLog, _ := os.OpenFile(debugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if debugLog != nil {
		_, _ = fmt.Fprintf(debugLog, "[%s] DISPATCHER STARTING: EUID=%d PID=%d Sock=%q\n", time.Now().Format("15:04:05"), os.Geteuid(), os.Getpid(), sockPath) // Debug logging is best-effort.
	}

	// Create a canary file to prove execution
	canaryPath := filepath.Join(os.TempDir(), fmt.Sprintf("f4-canary-%d.txt", os.Getpid()))
	// Same reasoning as the debug log, and worse: WriteFile truncates, so a
	// symlink planted here would have root empty the target rather than just
	// append to it.
	if canary, err := os.OpenFile(canaryPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0600); err == nil {
		_, _ = fmt.Fprintf(canary, "EUID=%d", os.Geteuid()) // The canary is diagnostic only.
		_ = canary.Close()
	}

	fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: STARTING (EUID=%d, PID=%d)\n", os.Geteuid(), os.Getpid())
	if os.Geteuid() != 0 {
		vtui.DebugLog("SUDO_DISPATCHER: FATAL: Not running as root (EUID: %d)", os.Geteuid())
		fmt.Fprintf(os.Stderr, "f4 dispatcher must run as root\n")
		os.Exit(1)
	}
	vtui.DebugLog("SUDO_DISPATCHER: Initializing on %q as root", sockPath)

	_ = os.Remove(sockPath) // ListenUnix reports an actionable bind error if the path still blocks startup.
	vtui.DebugLog("SUDO_DISPATCHER: Creating socket %q", sockPath)
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: ResolveUnixAddr failed: %v\n", err)
		os.Exit(1)
	}

	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: ListenUnix failed: %v\n", err)
		os.Exit(1)
	}
	if debugLog != nil {
		_, _ = fmt.Fprintf(debugLog, "[%s] DISPATCHER: Socket created.\n", time.Now().Format("15:04:05")) // Debug logging is best-effort.
	}

	fi, _ := os.Stat(sockPath)
	fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: Socket created. Initial perms: %v\n", fi.Mode())
	if debugLog != nil {
		_, _ = fmt.Fprintf(debugLog, "[%s] DISPATCHER: Initial perms: %v\n", time.Now().Format("15:04:05"), fi.Mode()) // Debug logging is best-effort.
	}

	fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: Restricting socket to uid=%d gid=%d...\n", invokingUID, invokingGID)
	if debugLog != nil {
		_, _ = fmt.Fprintf(debugLog, "[%s] DISPATCHER: Restricting socket to uid=%d gid=%d.\n", time.Now().Format("15:04:05"), invokingUID, invokingGID) // Debug logging is best-effort.
	}
	// Only the f4 process which invoked sudo may connect to the root dispatcher.
	// A world-writable socket would let another local user race the legitimate
	// client and issue arbitrary filesystem operations as root.
	err = os.Chown(sockPath, invokingUID, invokingGID)
	if err == nil {
		err = os.Chmod(sockPath, 0600)
	}
	if debugLog != nil {
		_, _ = fmt.Fprintf(debugLog, "[%s] DISPATCHER: Socket restriction result: %v\n", time.Now().Format("15:04:05"), err) // Debug logging is best-effort.
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: Socket restriction failed: %v\n", err)
		_ = l.Close()
		os.Exit(1)
	}

	// In this design, we accept a single connection, process its requests,
	// and exit when it drops (or when f4 finishes).
	conn, err := l.AcceptUnix()
	if err == nil {
		handleSudoClient(conn)
	}
	_ = l.Close() // The single-client dispatcher is done.
	if debugLog != nil {
		_ = debugLog.Close() // Best effort before the explicit process exit.
	}
	os.Exit(0)
}

func handleSudoClient(conn *net.UnixConn) {
	defer func() {
		_ = conn.Close() // Transport cleanup cannot change a completed request.
	}()

	for {
		var req SudoRequest
		vtui.DebugLog("SUDO_DISPATCHER: Waiting for message from client...")
		_, err := recvMsg(conn, &req)
		if err == nil {
			vtui.DebugLog("SUDO_DISPATCHER: Received Cmd=%d for Path=%q", req.Cmd, req.Path)
			fmt.Fprintf(os.Stderr, "SUDO_DISPATCHER: Received Cmd=%d for Path=%q\n", req.Cmd, req.Path)
		} else {
			vtui.DebugLog("SUDO_DISPATCHER: recvMsg error: %v", err)
		}
		if err != nil {
			return // Client disconnected or protocol error
		}

		resp := SudoResponse{}
		fd := -1
		var openedFile *os.File

		vtui.DebugLog("SUDO_DISPATCHER: Processing Cmd=%d, Path=%q", req.Cmd, req.Path)

		switch req.Cmd {
		case CmdPing:
			// Just return success

		case CmdOpen:
			fi, err := os.Stat(req.Path)
			if err == nil && (fi.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0) {
				resp.Error = "cannot open special file"
			} else {
				f, err := os.OpenFile(req.Path, req.Flags, os.FileMode(req.Mode))
				if err != nil {
					vtui.DebugLog("SUDO_DISPATCHER: Open(%q) FAILED: %v", req.Path, err)
					resp.Error = err.Error()
				} else {
					fd = int(f.Fd())
					openedFile = f
					vtui.DebugLog("SUDO_DISPATCHER: Open(%q) SUCCESS, FD=%d", req.Path, fd)
				}
			}

		case CmdStat:
			info, err := os.Stat(req.Path)
			if err != nil {
				resp.Error = err.Error()
			} else {
				resp.Item = VFSItem{
					KnownMetadata: MetadataExplicit | MetadataPermissions | MetadataMTime | MetadataHidden | MetadataExecutable, SizeKnown: true,
					UnixMode:     uint32(info.Mode().Perm()),
					Name:         info.Name(),
					Size:         info.Size(),
					IsDir:        info.IsDir(),
					MTime:        info.ModTime(),
					IsExecutable: info.Mode().Perm()&0111 != 0,
					IsHidden:     strings.HasPrefix(info.Name(), "."),
				}
				fillPlatformTimes(&resp.Item, info)
				fillPhysicalSizeCheap(&resp.Item, info)
			}

		case CmdMkDir:
			err := os.MkdirAll(req.Path, os.FileMode(req.Mode))
			if err != nil {
				resp.Error = err.Error()
			}

		case CmdRemove:
			err := os.RemoveAll(req.Path)
			if err != nil {
				resp.Error = err.Error()
			}

		case CmdRename:
			var err error
			if req.Flags&SudoRenameNoReplace != 0 {
				err = renameNoReplace(req.Path, req.Path2)
			} else {
				err = os.Rename(req.Path, req.Path2)
			}
			if err != nil {
				resp.Error = err.Error()
			}
		case CmdSetAttributes:
			// Apply all 3 metadata types at once under root
			err := os.Chmod(req.Path, os.FileMode(req.Item.UnixMode))
			if err == nil {
				err = os.Chown(req.Path, req.Item.Uid, req.Item.Gid)
			}
			if err == nil {
				err = os.Chtimes(req.Path, req.Item.ATime, req.Item.MTime)
			}
			if err == nil {
				err = applyPlatformAttributes(req.Path, req.Item)
			}
			if err != nil {
				resp.Error = err.Error()
			}

		case CmdReadDir:
			entries, err := os.ReadDir(req.Path)
			if err != nil {
				resp.Error = err.Error()
			} else {
				for _, e := range entries {
					info, _ := e.Info()
					var size int64
					var mtime time.Time
					var isExec bool
					if info != nil {
						size = info.Size()
						mtime = info.ModTime()
						isExec = info.Mode().Perm()&0111 != 0
					}
					isDir := e.IsDir()

					// Resolve symlinks/special objects to check if they are directories
					if !isDir && !e.Type().IsRegular() {
						if target, err := os.Stat(filepath.Join(req.Path, e.Name())); err == nil {
							isDir = target.IsDir()
						}
					}

					item := VFSItem{
						KnownMetadata: MetadataExplicit | MetadataHidden, SizeKnown: info != nil,
						IsSymlink:    e.Type()&os.ModeSymlink != 0,
						Name:         e.Name(),
						Size:         size,
						IsDir:        isDir,
						MTime:        mtime,
						IsExecutable: isExec,
						IsHidden:     strings.HasPrefix(e.Name(), "."),
					}
					if info != nil {
						item.UnixMode = uint32(info.Mode().Perm())
						item.KnownMetadata |= MetadataPermissions | MetadataMTime | MetadataExecutable
						fillPlatformTimes(&item, info)
						fillPhysicalSizeCheap(&item, info)
					}
					resp.Items = append(resp.Items, item)
				}
			}
		}

		err = sendMsg(conn, resp, fd)
		if openedFile != nil {
			_ = openedFile.Close() // sendMsg duplicated the descriptor for the client.
		}
		if err != nil {
			return
		}
	}
}
