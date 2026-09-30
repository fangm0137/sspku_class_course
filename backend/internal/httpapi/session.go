package httpapi

import "net/http"

// me reports the account behind the presented bearer token. RequireIdentity has
// already verified the signature and reloaded the account row, so the token
// itself is never echoed back.
func (s *Security) me(w http.ResponseWriter, r *http.Request) {
	jsonReply(w, 200, r.Context().Value(identityKey{}).(userIdentity))
}

// logout only confirms the presented token was valid. Bearer tokens are
// stateless, so ending the session means the client discards its copy; the
// server keeps nothing that could be replayed or deleted.
func (s *Security) logout(w http.ResponseWriter, r *http.Request) {
	jsonReply(w, 200, map[string]bool{"ok": true})
}
