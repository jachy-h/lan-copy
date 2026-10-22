// update-manifest creates a checksum manifest and mandatory Ed25519 signature.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	keyFile := flag.String("generate-key", "", "write a new base64 private key to this file, then print its public key")
	dir := flag.String("dir", "", "package directory")
	version := flag.String("version", "dev", "application version")
	goos := flag.String("os", "", "target OS")
	arch := flag.String("arch", "", "target architecture")
	signingKeyFile := flag.String("key-file", "", "private key file used when LAN_COPY_SIGNING_KEY is unset")
	publicKey := flag.String("public-key", "", "expected base64 public key (requires a matching signing key)")
	flag.Parse()
	fail := func(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	if *keyFile != "" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fail(err)
		}
		f, err := os.OpenFile(*keyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fail(err)
		}
		_, err = f.WriteString(base64.StdEncoding.EncodeToString(priv) + "\n")
		if err != nil {
			_ = f.Close()
			fail(err)
		}
		if err = f.Close(); err != nil {
			fail(err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
		return
	}
	if *dir == "" || *version == "" || *goos == "" || *arch == "" {
		fail(fmt.Errorf("-dir, -version, -os and -arch are required"))
	}
	if *publicKey == "" {
		fail(fmt.Errorf("-public-key is required: release packages must pin a trusted update key"))
	}
	binary := "lan-copy"
	if *goos == "windows" {
		binary += ".exe"
	}
	data, err := os.ReadFile(filepath.Join(*dir, binary))
	if err != nil {
		fail(err)
	}
	sum := sha256.Sum256(data)
	manifest := struct {
		App     string `json:"app"`
		Version string `json:"version"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
		Binary  string `json:"binary"`
		SHA256  string `json:"sha256"`
	}{"lan-copy", *version, *goos, *arch, binary, hex.EncodeToString(sum[:])}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail(err)
	}
	raw = append(raw, '\n')
	var signature []byte
	secret := os.Getenv("LAN_COPY_SIGNING_KEY")
	if secret == "" && *signingKeyFile != "" {
		data, err := os.ReadFile(*signingKeyFile)
		if err != nil {
			fail(fmt.Errorf("read signing key: %w", err))
		}
		secret = strings.TrimSpace(string(data))
	}
	if secret != "" {
		key, err := base64.StdEncoding.DecodeString(secret)
		if err != nil || len(key) != ed25519.PrivateKeySize {
			fail(fmt.Errorf("LAN_COPY_SIGNING_KEY must be a base64 Ed25519 private key"))
		}
		pub := base64.StdEncoding.EncodeToString(ed25519.PrivateKey(key).Public().(ed25519.PublicKey))
		if *publicKey != "" && pub != *publicKey {
			fail(fmt.Errorf("signing key does not match the pinned public key"))
		}
		signature = ed25519.Sign(key, raw)
	} else if *publicKey != "" {
		fail(fmt.Errorf("a pinned public key requires LAN_COPY_SIGNING_KEY or -key-file to sign the release"))
	}
	if err = os.WriteFile(filepath.Join(*dir, "update.json"), raw, 0644); err != nil {
		fail(err)
	}
	if signature != nil {
		if err = os.WriteFile(filepath.Join(*dir, "update.sig"), []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0644); err != nil {
			fail(err)
		}
	} else {
		_ = os.Remove(filepath.Join(*dir, "update.sig"))
	}
}
