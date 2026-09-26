package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// TokenKind は認証トークンの用途。
type TokenKind string

const (
	TokenSession       TokenKind = "session"
	TokenInvitation    TokenKind = "invitation"
	TokenPasswordReset TokenKind = "password_reset"
)

// newToken は、利用者に渡すトークンと、DB に保存するハッシュを返す。トークンそのものは保存しない（I-10）。
func newToken() (token string, hash []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token)
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
