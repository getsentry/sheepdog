package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
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

func (s sealer) seal(plaintext string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
}

func (s sealer) open(sealed []byte) (string, error) {
	size := s.aead.NonceSize()
	if len(sealed) < size {
		return "", errors.New("store: ciphertext is too short")
	}
	plaintext, err := s.aead.Open(nil, sealed[:size], sealed[size:], nil)
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
