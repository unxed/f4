// Diagnostic for unxed/f4#2157 (not for merging): how long the vtinput reader
// takes to turn a bracketed paste of 2000 lines into events.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/unxed/vtinput"
)

func main() {
	pr, pw, _ := os.Pipe()
	var b []byte
	b = append(b, "\x1b[200~"...)
	for i := 1; i <= 2000; i++ {
		b = append(b, strconv.Itoa(i)...)
		b = append(b, '\r')
	}
	b = append(b, "\x1b[201~"...)
	go func() {
		for i := 0; i < len(b); i += 4096 {
			j := i + 4096
			if j > len(b) {
				j = len(b)
			}
			_, _ = pw.Write(b[i:j])
		}
	}()
	r := vtinput.NewReader(pr, false)
	start := time.Now()
	n, ends := 0, 0
	for {
		ev, err := r.ReadEventTimeout(3 * time.Second)
		if err != nil || ev == nil {
			break
		}
		n++
		if ev.Type == vtinput.PasteEventType && !ev.PasteStart {
			ends++
			break
		}
	}
	fmt.Printf("READER: %d events, paste end seen=%d, %v\n", n, ends, time.Since(start))
}
