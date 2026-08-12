package vtvibe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

// Pack renders every file of the context folder as one deterministic text
// block. The markers are the ones vtvibe.md fixes in section 8.1, so the same
// text can be pasted by hand into any web chat.
//
// Two runs over the same tree produce byte identical output: there is no
// timestamp in the header on purpose.
func (s *Session) Pack() string {
	files := s.tree.walkFiles(ctxDir)
	var body bytes.Buffer
	var tree bytes.Buffer
	count, total := 0, 0
	var skipped []string

	for _, full := range files {
		rel := strings.TrimPrefix(full, ctxDir+"/")
		if looksSecret(rel) {
			skipped = append(skipped, rel)
			continue
		}
		data, ok := s.tree.readFile(full)
		if !ok {
			continue
		}
		count++
		total += len(data)
		fmt.Fprintf(&tree, "  %s\n", rel)
		fmt.Fprintf(&body, "\n=== BEGIN %s ===\n", rel)
		if isBinary(data) {
			sum := sha256.Sum256(data)
			fmt.Fprintf(&body, "<binary, %d bytes, sha256:%s>\n", len(data), hex.EncodeToString(sum[:]))
		} else {
			text := string(data)
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			body.WriteString(text)
		}
		fmt.Fprintf(&body, "=== END %s ===\n", rel)
	}

	if count == 0 && len(skipped) == 0 {
		return ""
	}

	var out bytes.Buffer
	out.WriteString("=== VTPACK 1 ===\n")
	fmt.Fprintf(&out, "root: ai://ctx\n")
	fmt.Fprintf(&out, "files: %d\n", count)
	fmt.Fprintf(&out, "bytes: %d\n", total)
	if len(skipped) > 0 {
		fmt.Fprintf(&out, "skipped (looks like a secret): %s\n", strings.Join(skipped, ", "))
	}
	out.WriteString("\n=== TREE ===\n")
	out.Write(tree.Bytes())
	out.WriteString("=== END TREE ===\n")
	out.Write(body.Bytes())
	out.WriteString("\n=== END VTPACK ===\n")
	return out.String()
}

// isBinary uses the same rule as vtvibe.md: a NUL byte in the first 8 KB.
func isBinary(data []byte) bool {
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// secretNames are files that must never leave the machine by accident. The
// rule of section 14 is "remove by default", not "warn and send anyway", so a
// match is dropped from the request and only its name is reported.
var secretNames = []string{
	".env", ".envrc", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
	".npmrc", ".pypirc", ".netrc", "credentials", "secrets",
}

var secretExts = []string{".pem", ".key", ".p12", ".pfx", ".keystore"}

func looksSecret(rel string) bool {
	base := strings.ToLower(path.Base(rel))
	for _, name := range secretNames {
		if base == name || strings.HasPrefix(base, name+".") {
			return true
		}
	}
	for _, ext := range secretExts {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(rel), "/.ssh/")
}
