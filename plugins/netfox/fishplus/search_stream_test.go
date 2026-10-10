package fishplus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func foundLine(name string) string { return "f f 3 1 1 1 644 0 0 /root/" + name + "\n" }

func TestFindStreamsBeforeSynchronousTerminator(t *testing.T) {
	release := make(chan struct{})
	// Register this after newMockPeer's cleanup so a failure always releases
	// the peer before its cleanup joins the blocked response handler.
	sess := newMockPeer(t, "ok FISHPLUS 1 mode:find", func(w io.Writer, token string, req mockRequest) {
		if _, err := fmt.Fprint(w, "M find\n"+foundLine("first.txt")); err != nil {
			t.Error(err)
			return
		}
		<-release
		if _, err := fmt.Fprintf(w, "%s.%s %s ok\n", foundLine("second.txt"), token, req.ID); err != nil {
			t.Error(err)
		}
	}, 2)
	defer close(release)
	if err := sess.Handshake(context.Background()); err != nil {
		t.Fatal(err)
	}
	received := make(chan Entry, 2)
	done := make(chan error, 1)
	go func() {
		hits, err := NewClient(sess).Find(context.Background(), "/root", FindOptions{Masks: []string{"*.txt"}, OnFound: func(e Entry) { received <- e }})
		if err == nil && len(hits) != 2 {
			err = fmt.Errorf("returned %d hits, want two", len(hits))
		}
		done <- err
	}()
	select {
	case e := <-received:
		if e.Name != "/root/first.txt" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("first hit waited for response completion")
	}
	select {
	case <-done:
		t.Fatal("search finished before the peer released the terminator")
	default:
	}
	// Send rather than close so the deferred close also works on failures.
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("find did not finish")
	}
	if len(received) != 1 {
		t.Fatalf("stream duplicated or lost hits: %d left", len(received))
	}
}

func TestFindJobStreamsAndPreservesPartialCancellation(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			polls := 0
			sess := newMockPeer(t, "ok FISHPLUS 1 mode:find jobs ffindjob", func(w io.Writer, token string, req mockRequest) {
				polls++
				payload := "S run -\nM find\nP 4 1 /root/first.txt\n" + foundLine("first.txt")
				if polls > 1 {
					payload = "S done 0\n" + foundLine("second.txt")
				}
				if _, err := fmt.Fprintf(w, "%s.%s %s ok\n", payload, token, req.ID); err != nil {
					t.Error(err)
				}
			})
			if err := sess.Handshake(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var hits []Entry
			var checkpoints []FindProgress
			result, err := NewClient(sess).followFind(ctx, 7, "/root", func(p FindProgress) { checkpoints = append(checkpoints, p) }, func(e Entry) {
				hits = append(hits, e)
				if stop {
					cancel()
				}
			})
			want := 2
			if stop {
				want = 1
			}
			if len(hits) != want || len(result) != want || len(checkpoints) != 1 {
				t.Fatalf("emitted=%v returned=%v progress=%v", hits, result, checkpoints)
			}
			if stop && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			if !stop && err != nil {
				t.Fatal(err)
			}
			if sess.Broken() {
				t.Fatal("a complete job poll was left out of sync")
			}
		})
	}
}

func TestStreamingResponseCancellationDrainsAndReusesSession(t *testing.T) {
	sess := newMockPeer(t, "ok FISHPLUS 1 mode:find", func(w io.Writer, token string, req mockRequest) {
		if _, err := fmt.Fprintf(w, "first\nsecond\n.%s %s ok\n", token, req.ID); err != nil {
			t.Error(err)
		}
	})
	if err := sess.Handshake(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	_, err := sess.ExecPathsLines(ctx, "test", nil, func(string) { count++; cancel() })
	if count != 1 || !errors.Is(err, context.Canceled) || sess.Broken() {
		t.Fatalf("count=%d err=%v broken=%v", count, err, sess.Broken())
	}
	resp, err := sess.Exec(context.Background(), "next")
	if err != nil || len(resp.Lines) != 2 {
		t.Fatalf("following request: resp=%+v err=%v", resp, err)
	}
}
