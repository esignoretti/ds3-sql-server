package auth

import (
	"crypto/sha256"
	"testing"

	"crypto/ed25519"
)

func TestKeyDerivation(t *testing.T) {
	password := "test-password"
	salt := "test-salt"

	seed := sha256.Sum256(append([]byte(password), []byte(salt)...))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	pubKey := privateKey.Public().(ed25519.PublicKey)

	msg := []byte("challenge-data")
	sig := ed25519.Sign(privateKey, msg)

	if !ed25519.Verify(pubKey, msg, sig) {
		t.Fatal("signature verification failed")
	}
}
