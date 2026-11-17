package plugin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	secretKeyFile = "secret.key"
	secretPrefix  = "enc:v1:"
	secretKeySize = 32
)

// SecretBox encrypts plugin environment values at rest with AES-256-GCM.
// The key comes from LAN_COPY_PLUGIN_SECRET_KEY when set, otherwise from a
// 0600 key file in the private application data directory.
type SecretBox struct {
	aead cipher.AEAD
}

func LoadSecretBox(dir, environmentKey string) (*SecretBox, error) {
	var key []byte
	if environmentKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(environmentKey)
		if err != nil || len(decoded) != secretKeySize {
			return nil, fmt.Errorf("%w: LAN_COPY_PLUGIN_SECRET_KEY must be base64 for 32 bytes", ErrSecretKeyUnavailable)
		}
		key = decoded
	} else {
		keyDir := filepath.Join(dir, "plugins")
		if err := os.MkdirAll(keyDir, 0o700); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSecretKeyUnavailable, err)
		}
		path := filepath.Join(keyDir, secretKeyFile)
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if len(data) != secretKeySize {
				return nil, fmt.Errorf("%w: invalid secret key file", ErrSecretKeyUnavailable)
			}
			key = data
		case errors.Is(err, os.ErrNotExist):
			key = make([]byte, secretKeySize)
			if _, err := rand.Read(key); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrSecretKeyUnavailable, err)
			}
			if err := createSecretKey(path, key); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrSecretKeyUnavailable, err)
			}
		default:
			return nil, fmt.Errorf("%w: %v", ErrSecretKeyUnavailable, err)
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSecretKeyUnavailable, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSecretKeyUnavailable, err)
	}
	return &SecretBox{aead: aead}, nil
}

func createSecretKey(path string, key []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(key)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return writeErr
	}
	return syncDir(filepath.Dir(path))
}

func (b *SecretBox) SealString(value string) (string, error) {
	if b == nil || b.aead == nil {
		return "", ErrSecretKeyUnavailable
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nil, nonce, []byte(value), nil)
	return secretPrefix + base64.StdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func (b *SecretBox) OpenString(value string) (string, error) {
	if b == nil || b.aead == nil {
		return "", ErrSecretKeyUnavailable
	}
	if len(value) < len(secretPrefix) || value[:len(secretPrefix)] != secretPrefix {
		return "", errors.New("plugin secret value is not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(value[len(secretPrefix):])
	if err != nil {
		return "", err
	}
	nonceSize := b.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("plugin secret value is truncated")
	}
	plain, err := b.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
