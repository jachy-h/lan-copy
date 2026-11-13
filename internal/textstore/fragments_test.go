package textstore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")

func TestImageDimensionsAreDetected(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 40, 20))); err != nil {
		t.Fatal(err)
	}
	mediaType, extension, width, height, ok := detectImage(encoded.Bytes())
	if !ok || mediaType != "image/png" || extension != ".png" || width != 40 || height != 20 {
		t.Fatalf("detected image = %q %q %dx%d, ok=%v", mediaType, extension, width, height, ok)
	}
}

func TestDuplicateImagesAreCollapsed(t *testing.T) {
	dir := t.TempDir()
	store := NewWithSettings(dir, Settings{ExpiryDays: 7})
	item, err := store.CreateFragment("", []ImageUpload{
		{Name: "image.png", Data: onePixelPNG},
		{Name: "image.png", Data: onePixelPNG},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Images) != 1 {
		t.Fatalf("duplicate uploads = %+v", item.Images)
	}
	items := []Fragment{{
		ID: "0011223344556677", Kind: "image", Created: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(time.Hour),
		Images: []Image{
			{Name: "image.png", MIME: "image/png", Size: 4, File: "0011223344556677-1.png"},
			{Name: "image.png", MIME: "image/png", Size: 4, File: "0011223344556677-2.png"},
		},
	}}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	listed, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || len(listed[0].Images) != 1 {
		t.Fatalf("historical duplicate images = %+v", listed)
	}
}

func TestMixedFragmentCreationAndDeletion(t *testing.T) {
	dir := t.TempDir()
	store := NewWithSettings(dir, Settings{ExpiryDays: 7})
	item, err := store.CreateFragment("图文说明", []ImageUpload{
		{Name: "第一张.png", Data: onePixelPNG},
		{Name: "第二张.png", Data: onePixelPNG},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != "mixed" || item.Content != "图文说明" || len(item.Images) != 2 {
		t.Fatalf("mixed fragment = %+v", item)
	}
	for index, image := range item.Images {
		if image.Width != 1 || image.Height != 1 || image.File == "" {
			t.Fatalf("image %d metadata = %+v", index, image)
		}
		if _, err := os.Stat(filepath.Join(dir, ImageDirName, image.File)); err != nil {
			t.Fatalf("image %d file missing: %v", index, err)
		}
	}
	file, opened, image, err := store.OpenImageAt(item.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len(onePixelPNG))
	if _, err := file.Read(data); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if opened.ID != item.ID || image.Name != "第二张.png" || !bytes.Equal(data, onePixelPNG) {
		t.Fatalf("opened second image = %+v", image)
	}
	if err := store.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	for _, image := range item.Images {
		if _, err := os.Stat(filepath.Join(dir, ImageDirName, image.File)); !os.IsNotExist(err) {
			t.Fatalf("image remains after delete: %v", err)
		}
	}
}

func TestFragmentImageLifecycle(t *testing.T) {
	dir := t.TempDir()
	store := NewWithSettings(dir, Settings{ExpiryDays: 7})
	item, err := store.CreateImage("截图.PNG", onePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != "image" || item.MIME != "image/png" || item.Name != "截图.png" || item.File == "" {
		t.Fatalf("unexpected image fragment: %+v", item)
	}
	if item.ExpiresAt.Sub(item.Created) < 6*24*time.Hour {
		t.Fatalf("unexpected expiry: %+v", item)
	}
	if _, err := os.Stat(filepath.Join(dir, ImageDirName, item.File)); err != nil {
		t.Fatalf("image file missing: %v", err)
	}
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("list = %+v, %v", items, err)
	}
	file, opened, err := store.OpenImage(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len(onePixelPNG))
	if _, err := file.Read(data); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if !bytes.Equal(data, onePixelPNG) || opened.ID != item.ID {
		t.Fatalf("opened image mismatch: %+v", opened)
	}
	if err := store.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ImageDirName, item.File)); !os.IsNotExist(err) {
		t.Fatalf("image file remains after delete: %v", err)
	}
}

func TestExpiredFragmentsArePrunedWithTheirImages(t *testing.T) {
	dir := t.TempDir()
	store := NewWithSettings(dir, Settings{ExpiryDays: 7})
	item, err := store.CreateImage("photo.png", onePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var items []Fragment
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	items[0].ExpiresAt = time.Now().Add(-time.Minute)
	raw, _ = json.Marshal(items)
	if err := os.WriteFile(filepath.Join(dir, FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	items, err = store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expired fragment still listed: %+v", items)
	}
	if _, err := os.Stat(filepath.Join(dir, ImageDirName, item.File)); !os.IsNotExist(err) {
		t.Fatalf("expired image remains on disk: %v", err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != "[]" {
		t.Fatalf("expired metadata remains: %s", stored)
	}
}

func TestUnsupportedImageIsRejected(t *testing.T) {
	store := New(t.TempDir())
	if _, err := store.CreateImage("notes.txt", []byte("not an image")); err != ErrUnsupportedImage {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedImage)
	}
}

func TestHistoricalFragmentsUseDefaultLifetime(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-8 * 24 * time.Hour)
	recent := time.Now().Add(-24 * time.Hour)
	items := []Fragment{
		{ID: "0011223344556677", Content: "expired history", Created: old},
		{ID: "8899aabbccddeeff", Content: "recent history", Created: recent},
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := New(dir)
	listed, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "8899aabbccddeeff" {
		t.Fatalf("historical cleanup = %+v", listed)
	}
	if listed[0].ExpiresAt.Sub(listed[0].Created) != DefaultExpiryDays*24*time.Hour {
		t.Fatalf("historical expiry = %+v", listed[0])
	}
	stored, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var persisted []Fragment
	if err := json.Unmarshal(stored, &persisted); err != nil || len(persisted) != 1 {
		t.Fatalf("persisted historical cleanup = %s, %v", stored, err)
	}
}

func TestFragmentSettingsPersist(t *testing.T) {
	dir := t.TempDir()
	if settings, err := LoadSettings(dir); err != nil || settings != DefaultSettings() {
		t.Fatalf("default settings = %+v, %v", settings, err)
	}
	if err := SaveSettings(dir, Settings{ExpiryDays: 14}); err != nil {
		t.Fatal(err)
	}
	if settings, err := LoadSettings(dir); err != nil || settings.ExpiryDays != 14 {
		t.Fatalf("loaded settings = %+v, %v", settings, err)
	}
	store := NewWithSettings(dir, Settings{ExpiryDays: 14})
	item, err := store.Create("two weeks")
	if err != nil {
		t.Fatal(err)
	}
	if days := item.ExpiresAt.Sub(item.Created).Hours() / 24; days < 13.9 || days > 14.1 {
		t.Fatalf("fragment expiry days = %v", days)
	}
	newSettings := Settings{ExpiryDays: 30}
	if err := store.Configure(newSettings); err != nil {
		t.Fatal(err)
	}
	if store.Settings() != newSettings {
		t.Fatalf("store settings = %+v", store.Settings())
	}
	if err := store.Configure(Settings{ExpiryDays: 0}); err != ErrInvalidSettings {
		t.Fatalf("invalid configure error = %v", err)
	}
}
