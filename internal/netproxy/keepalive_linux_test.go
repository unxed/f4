//go:build linux

package netproxy

import (
	"net"
	"syscall"
	"testing"
	"time"
)

// The kernel must actually be told the schedule, on a direct socket and on
// the socket that carries a CONNECT tunnel alike. Only Linux exposes the
// three values through plain getsockopt, so only Linux checks them.
func TestDialContextPinsTheKernelKeepAliveSchedule(t *testing.T) {
	sink := startSink(t)
	proxyHost, proxyPort, _ := net.SplitHostPort(fakeConnectProxy(t, "", sink).Addr().String())

	for name, s := range map[string]Settings{
		"direct": {Mode: ModeDirect},
		"http":   {Mode: ModeHTTP, Host: proxyHost, Port: proxyPort},
	} {
		t.Run(name, func(t *testing.T) {
			conn, err := s.Dial("tcp", sink)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer func() { _ = conn.Close() }()

			var tcp *net.TCPConn
			switch c := conn.(type) {
			case *net.TCPConn:
				tcp = c
			case *bufferedConn:
				tcp = c.Conn.(*net.TCPConn)
			default:
				t.Fatalf("unexpected conn type %T", conn)
			}
			raw, err := tcp.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			got := map[int]int{}
			var optErr error
			if err := raw.Control(func(fd uintptr) {
				for _, opt := range []int{syscall.SO_KEEPALIVE, syscall.TCP_KEEPIDLE, syscall.TCP_KEEPINTVL, syscall.TCP_KEEPCNT} {
					level := syscall.IPPROTO_TCP
					if opt == syscall.SO_KEEPALIVE {
						level = syscall.SOL_SOCKET
					}
					v, err := syscall.GetsockoptInt(int(fd), level, opt)
					if err != nil {
						optErr = err
						return
					}
					got[opt] = v
				}
			}); err != nil {
				t.Fatal(err)
			}
			if optErr != nil {
				t.Fatal(optErr)
			}
			want := map[int]int{
				syscall.SO_KEEPALIVE:  1,
				syscall.TCP_KEEPIDLE:  int(tcpKeepAlive.Idle / time.Second),
				syscall.TCP_KEEPINTVL: int(tcpKeepAlive.Interval / time.Second),
				syscall.TCP_KEEPCNT:   tcpKeepAlive.Count,
			}
			for opt, w := range want {
				if got[opt] != w {
					t.Errorf("sockopt %d = %d, want %d", opt, got[opt], w)
				}
			}
		})
	}
}

func startSink(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 1)
				for {
					if _, err := c.Read(buf); err != nil {
						_ = c.Close()
						return
					}
				}
			}()
		}
	}()
	return l.Addr().String()
}
