//go:build !windows

package vfs

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/unxed/vtui"
	"io"
	"math"
	"net"
	"os"
	"syscall"
)

// sendMsg serializes the payload and sends it over the Unix socket, attaching a file descriptor if provided.
func sendMsg(conn *net.UnixConn, msg any, fd int) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if uint64(len(data)) > math.MaxUint32 {
		return fmt.Errorf("sudo IPC message is too large: %d bytes", len(data))
	}

	lenBuf := make([]byte, 4)
	// #nosec G115 -- the message length was checked against MaxUint32 above.
	binary.LittleEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := conn.Write(lenBuf); err != nil {
		return err
	}

	var oob []byte
	if fd >= 0 {
		vtui.DebugLog("SUDO_IPC: Attaching FD=%d to message", fd)
		oob = syscall.UnixRights(fd)
	}

	n, _, err := conn.WriteMsgUnix(data, oob, nil)
	if err != nil {
		vtui.DebugLog("SUDO_IPC: WriteMsgUnix FAILED: %v", err)
		return err
	}
	// On a stream socket sendmsg may take only part of a big payload; the
	// descriptor rides with the first part, the rest goes as plain bytes.
	if n < len(data) {
		_, err = conn.Write(data[n:])
	}
	return err
}

// recvMsg reads a length-prefixed payload and parses it, extracting any passed file descriptor.
func recvMsg(conn *net.UnixConn, msg any) (*os.File, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, err
	}
	length := binary.LittleEndian.Uint32(lenBuf)
	vtui.DebugLog("SUDO_IPC: Incoming message length: %d", length)

	buf := make([]byte, length)
	oob := make([]byte, syscall.CmsgSpace(4))

	n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, err
	}

	// Guarantee we read the full payload if fragmented
	if n < len(buf) {
		if _, err := io.ReadFull(conn, buf[n:]); err != nil {
			return nil, err
		}
	}

	if err = json.Unmarshal(buf, msg); err != nil {
		return nil, err
	}

	var f *os.File
	if oobn > 0 {
		scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err == nil && len(scms) > 0 {
			fds, err := syscall.ParseUnixRights(&scms[0])
			if err == nil && len(fds) > 0 {
				vtui.DebugLog("SUDO_IPC: Received FD=%d via SCM_RIGHTS", fds[0])
				f = os.NewFile(uintptr(fds[0]), "sudo-fd")
			}
		} else {
			vtui.DebugLog("SUDO_IPC: Failed to parse OOB data: %v", err)
		}
	}

	return f, nil
}
