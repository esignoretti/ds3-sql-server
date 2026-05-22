package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/ed25519"
)

func TestKeyDerivation(t *testing.T) {
	password := "test-password"
	salt := base64.StdEncoding.EncodeToString([]byte("test-salt"))

	saltBytes, _ := base64.StdEncoding.DecodeString(salt)
	key := sha256.Sum256(append([]byte(password), saltBytes...))
	privKey := ed25519.NewKeyFromSeed(key[:])
	pubKey := privKey.Public().(ed25519.PublicKey)

	msg := []byte("challenge-data")
	sig := ed25519.Sign(privKey, msg)

	if !ed25519.Verify(pubKey, msg, sig) {
		t.Fatal("signature verification failed")
	}
}
