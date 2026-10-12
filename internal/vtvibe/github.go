package vtvibe

import "strings"

// The GitHub token (unxed/f4#1842, docs/VTVIBE.md § 19a.4, stage H6). It is
// bound to one dialog or, in the settings, to all of them; the commands the
// bot and the workers run get it as GH_TOKEN and GITHUB_TOKEN, the variables
// gh, git credential helpers and most GitHub tools read. It is never put into
// the prompt; a command the model runs can read it, as it can any variable of
// its environment.

// GitHubEnv is the environment that hands token to the commands; nil for an
// empty token, so the environment's own token, if any, is left alone.
func GitHubEnv(token string) []string {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return []string{"GH_TOKEN=" + token, "GITHUB_TOKEN=" + token}
}

// GitHubToken returns the dialog's own token; empty when it has none.
func (s *Session) GitHubToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.githubToken
}

// SetGitHubToken binds token to the dialog; an empty one unbinds it. It is
// kept with the dialog, whose file only the user can read.
func (s *Session) SetGitHubToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.githubToken = strings.TrimSpace(token)
	s.saveLocked()
}
