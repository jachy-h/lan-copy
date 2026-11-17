package plugin

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func nowUTC() time.Time { return time.Now().UTC() }

type statsEntry struct {
	stats *Stats
	at    time.Time
}

func randomID(prefix string) (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buffer), nil
}
