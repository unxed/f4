// Package embedded exposes README.md to the application.
//
// An embed directive can only reference paths inside its own directory, and
// README.md must stay in the repository root to render on GitHub. That is the
// whole reason this package exists in the root; nothing else belongs here.
package embedded

import _ "embed"

//go:embed README.md
var ReadmeMD string
