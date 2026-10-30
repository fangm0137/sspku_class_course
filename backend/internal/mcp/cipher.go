package mcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

// cipherBox 用 AES-256-GCM 把令牌加密后落库。密文格式为 nonce||ciphertext，
// nonce 每次随机，因此同一令牌两次写入的密文不同。
type cipherBox struct {
	aead cipher.AEAD
}

var errCipherUnavailable = errors.New("token cipher unavailable")

func newCipherBox(key []byte) (*cipherBox, error) {
	if len(key) != 32 {
		return nil, errCipherUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errCipherUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errCipherUnavailable
	}
	return &cipherBox{aead: aead}, nil
}

func (b *cipherBox) seal(plain string) ([]byte, error) {
	if b == nil {
		return nil, errCipherUnavailable
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errCipherUnavailable
	}
	return b.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (b *cipherBox) open(data []byte) (string, error) {
	if b == nil {
		return "", errCipherUnavailable
	}
	size := b.aead.NonceSize()
	if len(data) <= size {
		return "", errCipherUnavailable
	}
	plain, err := b.aead.Open(nil, data[:size], data[size:], nil)
	if err != nil {
		return "", errCipherUnavailable
	}
	return string(plain), nil
}
