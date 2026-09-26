package netfox

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"net"
	"sync"
	"testing"

	"github.com/pkg/sftp"
)

// sftpByteCountingConn counts bytes read off the wire, i.e. traffic flowing
// from the server to this end. That is the direction a random-access read
// must bound: proving ReadAt answers from an offset without the server
// streaming the whole remote file down first.
type sftpByteCountingConn struct {
	net.Conn

	mu    sync.Mutex
	readN int64
}

func (c *sftpByteCountingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	c.readN += int64(n)
	c.mu.Unlock()
	return n, err
}

func (c *sftpByteCountingConn) bytesRead() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readN
}

// startTestSFTPServer runs a real pkg/sftp request server, backed by its own
// in-memory test filesystem, over a real TCP loopback connection. It speaks
// SFTP's actual wire protocol with nothing simulated, the same spirit as
// FISH+'s tests against an in-memory peer and against a real local shell,
// just for SFTP's own binary protocol instead of a shell one.
func startTestSFTPServer(t *testing.T) (*sftp.Client, *sftpByteCountingConn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		server := sftp.NewRequestServer(conn, sftp.InMemHandler())
		_ = server.Serve()
		_ = server.Close()
	}()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	counted := &sftpByteCountingConn{Conn: conn}
	client, err := sftp.NewClientPipe(counted, counted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		<-serverDone
	})
	return client, counted
}

// TestSFTPFileReadAtIsRandomAccessNotFullDownload is the SFTP counterpart of
// FISH+'s TestFileReadAtAndCache: it proves that opening a large file over
// SFTP and reading a small window near its end costs a bounded amount of
// wire traffic rather than the whole file, which is what lets the editor
// open a huge remote file from the middle (docs/FISH+.md, Step 20).
//
// Unlike FISH+'s custom "read <offset> <length>" shell command, SFTP has
// this built into the protocol as SSH_FXP_READ, and pkg/sftp's *sftp.File
// already implements io.ReaderAt on top of it. sftpFileWrapper.ReadAt in
// sftp_vfs.go forwards straight to that, so this test also guards against a
// future change accidentally routing reads through something that buffers
// or downloads the whole file first.
func TestSFTPFileReadAtIsRandomAccessNotFullDownload(t *testing.T) {
	client, counted := startTestSFTPServer(t)

	const fileSize = 8 << 20 // 8MB: large enough that "downloaded it all" is obvious.
	content := make([]byte, fileSize)
	rand.New(rand.NewSource(42)).Read(content)

	wf, err := client.Create("/big.bin")
	if err != nil {
		t.Fatalf("create remote file: %v", err)
	}
	if _, err := wf.Write(content); err != nil {
		t.Fatalf("write remote file: %v", err)
	}
	if err := wf.Close(); err != nil {
		t.Fatalf("close remote file: %v", err)
	}

	v := &SFTPVFS{client: client}

	f, err := v.Open(context.Background(), "/big.bin")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = f.Close() }()

	if got := f.Size(); got != int64(fileSize) {
		t.Fatalf("Size() = %d, want %d", got, fileSize)
	}

	// Everything above (upload, Open, Stat) is done; only what ReadAt itself
	// costs on the wire should count from here.
	before := counted.bytesRead()

	const readLen = 4096
	off := int64(fileSize) - readLen - 12345 // well past the middle, near the end
	buf := make([]byte, readLen)
	n, err := f.ReadAt(context.Background(), buf, off)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != readLen {
		t.Fatalf("ReadAt returned %d bytes, want %d", n, readLen)
	}
	if !bytes.Equal(buf, content[off:off+readLen]) {
		t.Fatal("ReadAt returned the wrong bytes for the requested offset")
	}

	transferred := counted.bytesRead() - before
	// A random-access read costs roughly one SFTP round trip's worth of
	// traffic (the payload plus a small packet header), not the file: bound
	// it at a small multiple of the request instead of anywhere near the
	// 8MB file, so a regression that reads sequentially from the start
	// fails loudly rather than merely being slow in production.
	if transferred > readLen*4 {
		t.Fatalf("ReadAt of %d bytes at offset %d transferred %d bytes from the server, want it bounded near the request size, not the whole %d-byte file", readLen, off, transferred, fileSize)
	}
}
