package plugin

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// StagedImage is a validated upload saved in the private temporary directory.
// Callers must pass it to ImportStaged, which removes the file.
type StagedImage struct {
	Path     string
	Filename string
	Size     int64
	SHA256   string
}

func (m *Manager) StageImage(filename string, reader io.Reader) (*StagedImage, error) {
	if err := ValidateImageFilename(filename); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(m.store.TmpDir(), "upload-*.image")
	if err != nil {
		return nil, fmt.Errorf("create image staging file: %w", err)
	}
	path := file.Name()
	fail := func(err error) (*StagedImage, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		return fail(err)
	}
	buffered := bufio.NewReaderSize(reader, 64<<10)
	if err := DetectImageArchive(buffered); err != nil {
		return fail(err)
	}
	hasher := sha256.New()
	limited := io.LimitReader(buffered, m.maxImageBytes+1)
	size, err := io.Copy(io.MultiWriter(file, hasher), limited)
	if err != nil {
		return fail(fmt.Errorf("write image upload: %w", err))
	}
	if size > m.maxImageBytes {
		return fail(fmt.Errorf("%w: image archive exceeds %d bytes", ErrTooLarge, m.maxImageBytes))
	}
	if size == 0 {
		return fail(fmt.Errorf("%w: image archive is empty", ErrUnsupportedArchive))
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &StagedImage{
		Path: path, Filename: filepath.Base(filename), Size: size,
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func (m *Manager) DiscardStaged(staged *StagedImage) {
	if staged != nil {
		_ = os.Remove(staged.Path)
	}
}

// ImportStaged loads the archive, validates the single plugin manifest and
// records the image. It never installs or starts the plugin.
func (m *Manager) ImportStaged(ctx context.Context, staged *StagedImage, progress func(int, string)) (Image, error) {
	if staged == nil {
		return Image{}, errors.New("missing staged image")
	}
	defer m.DiscardStaged(staged)
	if progress != nil {
		progress(10, "正在导入镜像")
	}
	file, err := os.Open(staged.Path)
	if err != nil {
		return Image{}, err
	}
	defer file.Close()
	infos, err := m.runtime.ImportImage(ctx, file, ImportImageRequest{Source: staged.Filename})
	if err != nil {
		return Image{}, fmt.Errorf("import image: %w", err)
	}
	if progress != nil {
		progress(60, "正在校验插件 Manifest")
	}
	image, err := m.registerImage(ctx, infos, staged.SHA256)
	if err != nil {
		return Image{}, err
	}
	if progress != nil {
		progress(95, "正在保存镜像记录")
	}
	return image, nil
}

func (m *Manager) registerImage(ctx context.Context, infos []ImageInfo, archiveSHA string) (Image, error) {
	runtimeInfo, err := m.runtime.Info(ctx)
	if err != nil {
		return Image{}, err
	}
	type candidate struct {
		info     ImageInfo
		manifest Manifest
	}
	valid := make([]candidate, 0, len(infos))
	for _, info := range infos {
		manifest, err := LoadManifestFromLabels(info.Labels)
		if err != nil {
			continue
		}
		valid = append(valid, candidate{info: info, manifest: manifest})
	}
	switch {
	case len(valid) == 0:
		return Image{}, fmt.Errorf("%w: %s", ErrNoPluginManifest, ManifestLabel)
	case len(valid) > 1:
		return Image{}, fmt.Errorf("%w: archive contains multiple plugin images", ErrConflict)
	}
	selected := valid[0]
	if err := validateNonRootUser(selected.info.User); err != nil {
		return Image{}, err
	}
	if runtimeInfo.Architecture != "" && normalizeArch(selected.info.Architecture) != normalizeArch(runtimeInfo.Architecture) {
		return Image{}, fmt.Errorf("%w: image architecture %s does not match runtime %s", ErrInvalidManifest, selected.info.Architecture, runtimeInfo.Architecture)
	}
	if runtimeInfo.OS != "" && selected.info.OS != "" && !strings.EqualFold(selected.info.OS, runtimeInfo.OS) {
		return Image{}, fmt.Errorf("%w: image OS %s does not match runtime %s", ErrInvalidManifest, selected.info.OS, runtimeInfo.OS)
	}

	reference := ""
	if len(selected.info.References) > 0 {
		reference = selected.info.References[0]
	}
	if reference == "" {
		reference = selected.info.ID
	}
	name, tag, registry := splitReference(reference)
	digest := selected.info.Digest
	if digest == "" {
		digest = selected.info.ID
	}

	m.mu.Lock()
	for _, existing := range m.state.Images {
		if existing.RuntimeImageID == selected.info.ID && existing.PluginID == selected.manifest.ID {
			m.mu.Unlock()
			return existing, nil
		}
	}
	now := m.now()
	image := Image{
		PluginID: selected.manifest.ID, RuntimeImageID: selected.info.ID,
		Reference: reference, ImageName: name, ImageTag: tag, Digest: digest,
		Registry: registry, Architecture: selected.info.Architecture, OS: selected.info.OS,
		SizeBytes: selected.info.SizeBytes, Source: "upload", ArchiveSHA256: archiveSHA,
		Manifest: selected.manifest, CreatedAt: now,
	}
	image.ID, err = randomID("img-")
	if err != nil {
		return Image{}, err
	}
	if existingIndex := pluginIndex(m.state.Plugins, selected.manifest.ID); existingIndex >= 0 {
		pluginEntry := &m.state.Plugins[existingIndex]
		pluginEntry.Name = selected.manifest.Name
		pluginEntry.Description = selected.manifest.Description
		pluginEntry.Category = selected.manifest.Category
		pluginEntry.ProtocolVersion = selected.manifest.ProtocolVersion
		// CurrentVersion describes the installed image, not the newest archive.
		// Importing a newer image must not make a running instance look upgraded.
		if _, hasInstance := instanceForPlugin(m.state.Instances, selected.manifest.ID); !hasInstance {
			pluginEntry.CurrentVersion = selected.manifest.Version
		}
		pluginEntry.UpdatedAt = now
	} else {
		m.state.Plugins = append(m.state.Plugins, Plugin{
			ID: selected.manifest.ID, Name: selected.manifest.Name,
			Description: selected.manifest.Description, Category: selected.manifest.Category,
			ProtocolVersion: selected.manifest.ProtocolVersion,
			CurrentVersion:  selected.manifest.Version,
			Status:          StatusNotInstalled, CreatedAt: now, UpdatedAt: now,
		})
	}
	m.state.Images = append(m.state.Images, image)
	snapshot := m.state.Clone()
	m.mu.Unlock()
	if err := m.store.Save(snapshot); err != nil {
		return Image{}, err
	}
	return image, nil
}
func (m *Manager) Images() []Image {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Image{}, m.state.Images...)
}

func (m *Manager) Image(id string) (Image, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	image, ok := imageByID(m.state.Images, id)
	if !ok {
		return Image{}, fmt.Errorf("%w: image %s", ErrNotFound, id)
	}
	return image, nil
}

func (m *Manager) RemoveImage(ctx context.Context, id string) error {
	m.mu.Lock()
	image, ok := imageByID(m.state.Images, id)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: image %s", ErrNotFound, id)
	}
	for _, instance := range m.state.Instances {
		if instance.ImageID == id {
			m.mu.Unlock()
			return fmt.Errorf("%w: image is used by plugin %s", ErrImageInUse, instance.PluginID)
		}
	}
	m.mu.Unlock()

	reference := image.RuntimeImageID
	if reference == "" {
		reference = image.Reference
	}
	if err := m.runtime.RemoveImage(ctx, reference, false); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	m.mu.Lock()
	index := -1
	for i := range m.state.Images {
		if m.state.Images[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}
	m.state.Images = append(m.state.Images[:index], m.state.Images[index+1:]...)
	pluginID := image.PluginID
	if len(imagesForPlugin(m.state.Images, pluginID)) == 0 {
		if _, hasInstance := instanceForPlugin(m.state.Instances, pluginID); !hasInstance {
			if pluginIdx := pluginIndex(m.state.Plugins, pluginID); pluginIdx >= 0 {
				m.state.Plugins = append(m.state.Plugins[:pluginIdx], m.state.Plugins[pluginIdx+1:]...)
			}
		}
	}
	snapshot := m.state.Clone()
	m.mu.Unlock()
	return m.store.Save(snapshot)
}

func validateNonRootUser(user string) error {
	trimmed := strings.TrimSpace(user)
	if trimmed == "" {
		return fmt.Errorf("%w: image must declare a non-root USER", ErrInvalidManifest)
	}
	userPart := strings.SplitN(trimmed, ":", 2)[0]
	if userPart == "0" || strings.EqualFold(userPart, "root") {
		return fmt.Errorf("%w: image runs as root", ErrInvalidManifest)
	}
	return nil
}

func normalizeArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "x86-64":
		return "amd64"
	case "aarch64", "arm64v8":
		return "arm64"
	case "i386", "i686":
		return "386"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

func splitReference(reference string) (name, tag, registry string) {
	reference = strings.TrimSpace(reference)
	if at := strings.LastIndex(reference, "@"); at >= 0 {
		reference = reference[:at]
	}
	name = reference
	if colon := strings.LastIndex(reference, ":"); colon > strings.LastIndex(reference, "/") {
		name = reference[:colon]
		tag = reference[colon+1:]
	}
	if slash := strings.Index(name, "/"); slash > 0 {
		first := name[:slash]
		if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
			registry = first
		}
	}
	return name, tag, registry
}
