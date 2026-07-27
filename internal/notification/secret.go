package notification

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

type secretBox struct {
	aead cipher.AEAD
}

func newSecretBox(material []byte) (*secretBox, error) {
	key := sha256.Sum256(append([]byte("autobuff-notification-secret:"), material...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretBox{aead: aead}, nil
}

func (b *secretBox) seal(value string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := b.aead.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func (b *secretBox) open(value string) (string, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("decode notification secret: %w", err)
	}
	nonceSize := b.aead.NonceSize()
	if len(payload) <= nonceSize {
		return "", fmt.Errorf("notification secret is truncated")
	}
	plaintext, err := b.aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt notification secret: %w", err)
	}
	return string(plaintext), nil
}
