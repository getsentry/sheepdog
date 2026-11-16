package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

type sealer struct {
	aead cipher.AEAD
}

func newSealer(key []byte) (sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return sealer{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return sealer{}, err
	}
	return sealer{aead: aead}, nil
}

// The associated data binds a value to its user and column, so a value copied to another row does not decrypt.
func associatedData(userID int64, column string) []byte {
	return fmt.Appendf(nil, "%s:%d", column, userID)
}

func (s sealer) seal(plaintext string, userID int64, column string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, []byte(plaintext), associatedData(userID, column))
}

func (s sealer) open(sealed []byte, userID int64, column string) (string, error) {
	size := s.aead.NonceSize()
	if len(sealed) < size {
		return "", errors.New("store: ciphertext is too short")
	}
	plaintext, err := s.aead.Open(nil, sealed[:size], sealed[size:], associatedData(userID, column))
	if err != nil {
		return "", errors.New("store: cannot decrypt a secret; is REVIEWQ_SECRET_KEY the same key that wrote the database?")
	}
	return string(plaintext), nil
}

func newSessionToken() string {
	token := make([]byte, 32)
	rand.Read(token)
	return base64.RawURLEncoding.EncodeToString(token)
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
