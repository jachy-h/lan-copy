package textstore

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	MaxTextChars   = 100000
	MaxTextBytes   = 1 << 20
	FileName       = ".lan-copy-texts.json"
	BackupFileName = ".lan-copy-texts.backup"
	MaxCount       = 1000
	MaxStoreBytes  = 16 << 20

	ImageDirName           = ".lan-copy-fragment-images"
	SettingsFileName       = ".lan-copy-fragments-settings.json"
	SettingsBackupFileName = ".lan-copy-fragments-settings.backup"
	DefaultExpiryDays      = 7
	MinExpiryDays          = 1
	MaxExpiryDays          = 365
	MaxImageBytes          = 20 << 20
	MaxImageStoreBytes     = 256 << 20
	MaxImagesPerFragment   = 6
)

var (
	ErrEmpty            = errors.New("empty fragment")
	ErrTextLimit        = errors.New("text size limit")
	ErrCountLimit       = errors.New("fragment count limit")
	ErrStoreLimit       = errors.New("fragment store size limit")
	ErrNotFound         = errors.New("fragment not found")
	ErrImageLimit       = errors.New("image size limit")
	ErrImageCountLimit  = errors.New("image count limit")
	ErrUnsupportedImage = errors.New("unsupported image type")
	ErrInvalidSettings  = errors.New("invalid fragment settings")
)

// Image is one image attached to a fragment. File is relative to the private
// fragment image directory and never comes from a client-supplied path.
type Image struct {
	Name   string `json:"name,omitempty"`
	MIME   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	File   string `json:"file"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// ImageUpload carries one image through the create path before persistence.
type ImageUpload struct {
	Name string
	Data []byte
}

// Fragment is a transient text and/or image share. Existing text-only records
// without Kind remain readable as text; legacy single-image fields are
// normalized into Images when read.
type Fragment struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content,omitempty"`
	Name      string    `json:"name,omitempty"`
	MIME      string    `json:"mime,omitempty"`
	Size      int64     `json:"size,omitempty"`
	File      string    `json:"file,omitempty"`
	Images    []Image   `json:"images,omitempty"`
	Created   time.Time `json:"created"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Snippet remains an alias for callers that still use the original name.
type Snippet = Fragment

type Settings struct {
	ExpiryDays int `json:"expiryDays"`
}

func DefaultSettings() Settings { return Settings{ExpiryDays: DefaultExpiryDays} }

func (s Settings) Valid() bool {
	return s.ExpiryDays >= MinExpiryDays && s.ExpiryDays <= MaxExpiryDays
}

type Store struct {
	dir      string
	mu       sync.Mutex
	settings Settings
}

func New(dir string) *Store { return &Store{dir: dir, settings: DefaultSettings()} }

func NewWithSettings(dir string, settings Settings) *Store {
	if !settings.Valid() {
		settings = DefaultSettings()
	}
	return &Store{dir: dir, settings: settings}
}

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *Store) Configure(settings Settings) error {
	if !settings.Valid() {
		return ErrInvalidSettings
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := SaveSettings(s.dir, settings); err != nil {
		return err
	}
	s.settings = settings
	return nil
}

func LoadSettings(dir string) (Settings, error) {
	name := filepath.Join(dir, SettingsFileName)
	backup := filepath.Join(dir, SettingsBackupFileName)
	if _, statErr := os.Lstat(name); errors.Is(statErr, os.ErrNotExist) {
		if _, backupErr := os.Lstat(backup); backupErr == nil {
			if err := os.Rename(backup, name); err != nil {
				return Settings{}, err
			}
			if err := syncDirectory(dir); err != nil {
				return Settings{}, err
			}
		} else if !errors.Is(backupErr, os.ErrNotExist) {
			return Settings{}, backupErr
		}
	} else if statErr != nil {
		return Settings{}, statErr
	} else if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Settings{}, err
	}
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Settings{}, errors.New("trailing fragment settings data")
	}
	if !settings.Valid() {
		return Settings{}, ErrInvalidSettings
	}
	return settings, nil
}

func SaveSettings(dir string, settings Settings) error {
	if !settings.Valid() {
		return ErrInvalidSettings
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".lan-copy-fragments-settings-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	destination := filepath.Join(dir, SettingsFileName)
	backup := filepath.Join(dir, SettingsBackupFileName)
	oldExists := false
	if _, statErr := os.Lstat(destination); statErr == nil {
		if err = os.Rename(destination, backup); err != nil {
			return err
		}
		oldExists = true
		if err = syncDirectory(dir); err != nil {
			return errors.Join(err, os.Rename(backup, destination), syncDirectory(dir))
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err = os.Rename(name, destination); err != nil {
		if oldExists {
			return errors.Join(err, os.Rename(backup, destination), syncDirectory(dir))
		}
		return err
	}
	if err = syncDirectory(dir); err != nil {
		rollbackErr := os.Remove(destination)
		if oldExists {
			rollbackErr = errors.Join(rollbackErr, os.Rename(backup, destination))
		}
		return errors.Join(err, rollbackErr, syncDirectory(dir))
	}
	if oldExists {
		if err = os.Remove(backup); err != nil {
			return err
		}
		return syncDirectory(dir)
	}
	return nil
}

func (s *Store) List() ([]Fragment, error) {
	s.mu.Lock()
	items, err := s.readLocked()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	kept, expired := pruneExpired(items, time.Now())
	if len(expired) > 0 {
		if err := writeTextsWithOps(s.dir, kept, defaultTextStoreOps()); err != nil {
			log.Printf("异常: 清理到期碎片失败: %v", err)
		} else {
			log.Printf("已清理 %d 条到期碎片", len(expired))
			s.removeImages(expired)
		}
	}
	s.mu.Unlock()
	sort.Slice(kept, func(i, j int) bool { return kept[i].Created.After(kept[j].Created) })
	return kept, nil
}

func (s *Store) Create(content string) (Fragment, error) {
	return s.CreateFragment(content, nil)
}

// CreateFragment stores text and any number of images as one share. Images are
// written before metadata is committed; failures remove files that were just
// written so no visible fragment points at missing data.
func (s *Store) CreateFragment(content string, uploads []ImageUpload) (Fragment, error) {
	hasText := strings.TrimSpace(content) != ""
	if !hasText && len(uploads) == 0 {
		return Fragment{}, ErrEmpty
	}
	if hasText && (utf8.RuneCountInString(content) > MaxTextChars || len([]byte(content)) > MaxTextBytes) {
		return Fragment{}, ErrTextLimit
	}
	if !hasText {
		content = ""
	}
	if len(uploads) > MaxImagesPerFragment {
		return Fragment{}, ErrImageCountLimit
	}
	id, err := newID()
	if err != nil {
		return Fragment{}, err
	}
	prepared := make([]Image, 0, len(uploads))
	payloads := make([][]byte, 0, len(uploads))
	var totalImageBytes int64
	seenUploads := make(map[string]bool, len(uploads))
	for _, upload := range uploads {
		if len(upload.Data) == 0 {
			return Fragment{}, ErrEmpty
		}
		if len(upload.Data) > MaxImageBytes {
			return Fragment{}, ErrImageLimit
		}
		mediaType, extension, width, height, ok := detectImage(upload.Data)
		if !ok {
			return Fragment{}, ErrUnsupportedImage
		}
		name := fragmentImageName(upload.Name, extension)
		key := name + "\x00" + strconv.Itoa(len(upload.Data)) + "\x00" + mediaType
		if seenUploads[key] {
			continue
		}
		seenUploads[key] = true
		prepared = append(prepared, Image{
			Name: name, MIME: mediaType, Size: int64(len(upload.Data)),
			File: id + "-" + strconv.Itoa(len(prepared)+1) + extension, Width: width, Height: height,
		})
		payloads = append(payloads, upload.Data)
		totalImageBytes += int64(len(upload.Data))
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.readLocked()
	if err != nil {
		return Fragment{}, err
	}
	items, expired := pruneExpired(items, now)
	if len(items) >= MaxCount {
		return Fragment{}, ErrCountLimit
	}
	if fragmentImageBytes(items)+totalImageBytes > MaxImageStoreBytes {
		return Fragment{}, ErrStoreLimit
	}
	written := make([]string, 0, len(prepared))
	for index, image := range prepared {
		if err := writeImageFile(s.dir, image.File, payloads[index]); err != nil {
			removeImageFilesByNames(s.dir, written)
			return Fragment{}, err
		}
		written = append(written, image.File)
	}
	item := Fragment{
		ID: id, Content: content, Images: prepared, Created: now,
		ExpiresAt: now.Add(time.Duration(s.settings.ExpiryDays) * 24 * time.Hour),
	}
	switch {
	case len(prepared) > 0 && hasText:
		item.Kind = "mixed"
	case len(prepared) > 0:
		item.Kind = "image"
	default:
		item.Kind = "text"
	}
	if len(prepared) == 1 && !hasText {
		// Keep the original single-image fields readable by older clients.
		image := prepared[0]
		item.Name, item.MIME, item.Size, item.File = image.Name, image.MIME, image.Size, image.File
	}
	items = append(items, item)
	if err := s.writeLocked(items); err != nil {
		removeImageFilesByNames(s.dir, written)
		return Fragment{}, err
	}
	s.removeImages(expired)
	return item, nil
}

func (s *Store) CreateImage(name string, data []byte) (Fragment, error) {
	return s.CreateFragment("", []ImageUpload{{Name: name, Data: data}})
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.readLocked()
	if err != nil {
		return err
	}
	var removed Fragment
	found := false
	kept := items[:0]
	for _, item := range items {
		if item.ID == id {
			removed, found = item, true
			continue
		}
		kept = append(kept, item)
	}
	if !found {
		return ErrNotFound
	}
	if err := s.writeLocked(kept); err != nil {
		return err
	}
	s.removeImages([]Fragment{removed})
	return nil
}

// Get returns one unexpired fragment without opening any image files.
func (s *Store) Get(id string) (Fragment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.readLocked()
	if err != nil {
		return Fragment{}, err
	}
	for _, item := range items {
		if item.ID != id {
			continue
		}
		if !item.ExpiresAt.IsZero() && !item.ExpiresAt.After(time.Now()) {
			return Fragment{}, ErrNotFound
		}
		return item, nil
	}
	return Fragment{}, ErrNotFound
}

// OpenImageAt opens one image of a fragment, preserving the original array
// order used by the web UI.
func (s *Store) OpenImageAt(id string, index int) (*os.File, Fragment, Image, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.readLocked()
	if err != nil {
		return nil, Fragment{}, Image{}, err
	}
	for _, item := range items {
		if item.ID != id {
			continue
		}
		if !item.ExpiresAt.IsZero() && !item.ExpiresAt.After(time.Now()) {
			return nil, Fragment{}, Image{}, ErrNotFound
		}
		images := fragmentImages(item)
		if index < 0 || index >= len(images) {
			return nil, Fragment{}, Image{}, ErrNotFound
		}
		image := images[index]
		if image.File == "" || filepath.Base(image.File) != image.File {
			return nil, Fragment{}, Image{}, ErrNotFound
		}
		file, err := os.Open(filepath.Join(s.dir, ImageDirName, image.File))
		if errors.Is(err, os.ErrNotExist) {
			return nil, Fragment{}, Image{}, ErrNotFound
		}
		return file, item, image, err
	}
	return nil, Fragment{}, Image{}, ErrNotFound
}

// OpenImage opens the first image of a fragment for the legacy endpoint.
func (s *Store) OpenImage(id string) (*os.File, Fragment, error) {
	file, item, _, err := s.OpenImageAt(id, 0)
	return file, item, err
}

func (s *Store) readLocked() ([]Fragment, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return []Fragment{}, nil
	}
	if err != nil {
		return nil, err
	}
	var items []Fragment
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].Kind == "" {
			if items[index].File != "" {
				items[index].Kind = "image"
			} else {
				items[index].Kind = "text"
			}
		}
		if len(items[index].Images) == 0 && items[index].File != "" {
			items[index].Images = fragmentImages(items[index])
		}
		items[index].Images = dedupeImages(items[index].Images)
		if items[index].ExpiresAt.IsZero() {
			// Historical records predate expiry. Apply the default lifetime from
			// their creation time so old, already-expired shares disappear.
			if items[index].Created.IsZero() {
				items[index].Created = time.Now()
			}
			items[index].ExpiresAt = items[index].Created.Add(DefaultExpiryDays * 24 * time.Hour)
		}
	}
	return items, nil
}

func (s *Store) writeLocked(items []Fragment) error {
	return writeTextsWithOps(s.dir, items, defaultTextStoreOps())
}

func pruneExpired(items []Fragment, now time.Time) ([]Fragment, []Fragment) {
	kept := make([]Fragment, 0, len(items))
	expired := make([]Fragment, 0)
	for _, item := range items {
		if !item.ExpiresAt.IsZero() && !item.ExpiresAt.After(now) {
			expired = append(expired, item)
			continue
		}
		kept = append(kept, item)
	}
	return kept, expired
}

func (s *Store) removeImages(items []Fragment) {
	for _, item := range items {
		for _, image := range fragmentImages(item) {
			_ = removeImageFile(s.dir, image)
		}
	}
}

func fragmentImages(item Fragment) []Image {
	if len(item.Images) > 0 {
		return item.Images
	}
	if item.File == "" {
		return nil
	}
	return []Image{{Name: item.Name, MIME: item.MIME, Size: item.Size, File: item.File}}
}

func fragmentImageBytes(items []Fragment) int64 {
	var total int64
	for _, item := range items {
		for _, image := range fragmentImages(item) {
			total += image.Size
		}
	}
	return total
}

// dedupeImages hides accidental duplicate attachments created by older
// releases (the same clipboard image exposed through both files and items).
func dedupeImages(images []Image) []Image {
	if len(images) < 2 {
		return images
	}
	seen := make(map[string]bool, len(images))
	kept := make([]Image, 0, len(images))
	for _, image := range images {
		key := image.Name + "\x00" + strconv.FormatInt(image.Size, 10) + "\x00" + image.MIME
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, image)
	}
	return kept
}

func newID() (string, error) {
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(idBytes), nil
}

func fragmentImageName(name, extension string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune(`/:*?"<>|`, r) {
			return -1
		}
		return r
	}, name)
	cleaned = strings.TrimSpace(strings.TrimSuffix(cleaned, "."))
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "图片" + extension
	}
	base := strings.TrimSuffix(cleaned, filepath.Ext(cleaned))
	if base == "" {
		base = "图片"
	}
	if runes := []rune(base); len(runes) > 80 {
		base = string(runes[:80])
	}
	return base + extension
}

func detectImage(data []byte) (string, string, int, int, bool) {
	mediaType := http.DetectContentType(data)
	var extension string
	switch mediaType {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/gif":
		extension = ".gif"
	case "image/webp":
		extension = ".webp"
	case "image/bmp":
		extension = ".bmp"
	default:
		return "", "", 0, 0, false
	}
	width, height := imageDimensions(data, mediaType)
	return mediaType, extension, width, height, true
}

func imageDimensions(data []byte, mediaType string) (int, int) {
	switch mediaType {
	case "image/png":
		if len(data) >= 24 {
			return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24]))
		}
	case "image/gif":
		if len(data) >= 10 {
			return int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
		}
	case "image/bmp":
		if len(data) >= 26 {
			width := int(int32(binary.LittleEndian.Uint32(data[18:22])))
			height := int(int32(binary.LittleEndian.Uint32(data[22:26])))
			if width < 0 {
				width = -width
			}
			if height < 0 {
				height = -height
			}
			return width, height
		}
	case "image/webp":
		return webpDimensions(data)
	case "image/jpeg":
		return jpegDimensions(data)
	}
	return 0, 0
}

func webpDimensions(data []byte) (int, int) {
	if len(data) < 30 {
		return 0, 0
	}
	switch string(data[12:16]) {
	case "VP8X":
		width := 1 + int(uint32(data[24])|uint32(data[25])<<8|uint32(data[26])<<16)
		height := 1 + int(uint32(data[27])|uint32(data[28])<<8|uint32(data[29])<<16)
		return width, height
	case "VP8 ":
		if data[23] == 0x9d && data[24] == 0x01 && data[25] == 0x2a {
			return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
		}
	case "VP8L":
		if data[20] == 0x2f {
			bits := binary.LittleEndian.Uint32(data[21:25])
			return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1
		}
	}
	return 0, 0
}

func jpegDimensions(data []byte) (int, int) {
	for index := 2; index+9 < len(data); {
		if data[index] != 0xff {
			index++
			continue
		}
		marker := data[index+1]
		if marker == 0xd8 || marker == 0xd9 || (marker >= 0xd0 && marker <= 0xd7) {
			index += 2
			continue
		}
		if index+4 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint16(data[index+2 : index+4]))
		if length < 2 {
			break
		}
		if (marker >= 0xc0 && marker <= 0xc3) || (marker >= 0xc5 && marker <= 0xc7) || (marker >= 0xc9 && marker <= 0xcb) || (marker >= 0xcd && marker <= 0xcf) {
			height := int(binary.BigEndian.Uint16(data[index+5 : index+7]))
			width := int(binary.BigEndian.Uint16(data[index+7 : index+9]))
			return width, height
		}
		index += 2 + length
	}
	return 0, 0
}

func writeImageFile(dir, name string, data []byte) error {
	imageDir := filepath.Join(dir, ImageDirName)
	if err := os.MkdirAll(imageDir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(imageDir, ".fragment-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, filepath.Join(imageDir, name)); err != nil {
		return err
	}
	return syncDirectory(imageDir)
}

func removeImageFile(dir string, image Image) error {
	if image.File == "" || filepath.Base(image.File) != image.File {
		return nil
	}
	if err := os.Remove(filepath.Join(dir, ImageDirName, image.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeImageFilesByNames(dir string, names []string) {
	for _, name := range names {
		_ = removeImageFile(dir, Image{File: name})
	}
}

type textStoreOps struct {
	write    func(*os.File, []byte) (int, error)
	syncFile func(*os.File) error
	close    func(*os.File) error
	rename   func(string, string) error
	remove   func(string) error
	syncDir  func(string) error
}

func defaultTextStoreOps() textStoreOps {
	return textStoreOps{write: (*os.File).Write, syncFile: (*os.File).Sync, close: (*os.File).Close, rename: os.Rename, remove: os.Remove, syncDir: syncDirectory}
}

func writeTextsWithOps(dir string, texts []Fragment, ops textStoreOps) error {
	data, err := json.Marshal(texts)
	if err != nil {
		return err
	}
	if len(texts) > MaxCount || len(data) > MaxStoreBytes {
		return fs.ErrInvalid
	}
	tmp, err := os.CreateTemp(dir, ".lan-copy-texts-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer ops.remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = ops.close(tmp)
		return err
	}
	if _, err := ops.write(tmp, data); err != nil {
		_ = ops.close(tmp)
		return err
	}
	if err := ops.syncFile(tmp); err != nil {
		_ = ops.close(tmp)
		return err
	}
	if err := ops.close(tmp); err != nil {
		return err
	}
	destination := filepath.Join(dir, FileName)
	backup := filepath.Join(dir, BackupFileName)
	// Always preserve the old file until the new directory entry is synced.
	if err := ops.remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oldExists := false
	if _, err := os.Lstat(destination); err == nil {
		if err := ops.rename(destination, backup); err != nil {
			return err
		}
		oldExists = true
		if err := ops.syncDir(dir); err != nil {
			return errors.Join(err, ops.rename(backup, destination), ops.syncDir(dir))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ops.rename(tmpName, destination); err != nil {
		if oldExists {
			return errors.Join(err, ops.rename(backup, destination), ops.syncDir(dir))
		}
		return err
	}
	if err := ops.syncDir(dir); err != nil {
		rollbackErr := ops.remove(destination)
		if oldExists {
			rollbackErr = errors.Join(rollbackErr, ops.rename(backup, destination))
		}
		rollbackErr = errors.Join(rollbackErr, ops.syncDir(dir))
		return errors.Join(err, rollbackErr)
	}
	if oldExists {
		if err := ops.remove(backup); err != nil {
			return err // New complete store is installed; backup is safe to clean on startup.
		}
		if err := ops.syncDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func Recover(dir string) error {
	destination := filepath.Join(dir, FileName)
	backup := filepath.Join(dir, BackupFileName)
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncDirectory(dir); err != nil {
			return err
		}
		if err := cleanupInterruptedTextWrites(dir); err != nil {
			return err
		}
		return cleanupOrphanImages(dir, destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(backup); err == nil {
		if err := os.Rename(backup, destination); err != nil {
			return err
		}
		log.Printf("异常恢复: 已从备份恢复共享文本库")
		if err := syncDirectory(dir); err != nil {
			return err
		}
		if err := cleanupInterruptedTextWrites(dir); err != nil {
			return err
		}
		return cleanupOrphanImages(dir, destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := cleanupInterruptedTextWrites(dir); err != nil {
		return err
	}
	return cleanupOrphanImages(dir, "")
}

func cleanupInterruptedTextWrites(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".lan-copy-texts-") {
			continue
		}
		log.Printf("异常: 发现未完成的文本库临时文件，名称=%s；将清理", entry.Name())
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("异常: 清理文本库临时文件失败，名称=%s 错误=%v", entry.Name(), err)
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(dir)
	}
	return nil
}

func cleanupOrphanImages(dir, metadata string) error {
	keep := make(map[string]bool)
	if metadata != "" {
		data, err := os.ReadFile(metadata)
		if err != nil {
			return err
		}
		var items []Fragment
		if err := json.Unmarshal(data, &items); err != nil {
			return nil // Do not delete unreferenced data when metadata is unreadable.
		}
		for _, item := range items {
			for _, image := range fragmentImages(item) {
				if image.File != "" && filepath.Base(image.File) == image.File {
					keep[image.File] = true
				}
			}
		}
	}
	imageDir := filepath.Join(dir, ImageDirName)
	entries, err := os.ReadDir(imageDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if entry.IsDir() || keep[entry.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(imageDir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("异常: 清理碎片图片失败，名称=%s 错误=%v", entry.Name(), err)
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(imageDir)
	}
	return nil
}
