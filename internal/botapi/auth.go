package botapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"runtime/debug"
	"strings"
)

// NewToken returns 32 random bytes as hex: a bearer token, or an identifier
// nobody can guess.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// BearerMatches reports whether a request carries token as its bearer
// credential. Both sides are hashed first, so the comparison takes the same
// time whatever either one's length; an empty token matches nothing.
func BearerMatches(r *http.Request, token string) bool {
	presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	want, got := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(presented))
	return ok && token != "" && subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

// BuildVersion is the release a binary reports: the version the linker set,
// or the module version go install recorded, or "dev".
func BuildVersion(linked string) string {
	if linked != "" && linked != "dev" {
		return linked
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
