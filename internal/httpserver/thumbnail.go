package httpserver

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// Bound simultaneous full-resolution decodes independently of file transfers.
var thumbnailSlots = make(chan struct{}, 2)

func (s *Server) thumbnailFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	_, err := s.files.Path(name)
	if err != nil || name == "" {
		filesystemError(w, errOrInvalid(err))
		return
	}
	_, info, err := s.files.RegularPath(name)
	if err != nil {
		if err == fs.ErrNotExist {
			writeError(w, 404, "文件不存在")
			return
		}
		filesystemError(w, err)
		return
	}
	if !strings.HasPrefix(previewContentType(name), "image/") {
		writeError(w, 415, "此文件没有图片缩略图")
		return
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif":
		// These formats can be resized using only the Go standard library.
	default:
		// SVG stays vector-based. Other browser image formats use lazy image loading.
		http.Redirect(w, r, "/preview-files/"+url.PathEscape(name), http.StatusTemporaryRedirect)
		return
	}
	if info.Size() > 32<<20 {
		writeError(w, 415, "图片较大，请点击文件名预览")
		return
	}
	tag := fmt.Sprintf("\"%x-%x\"", info.ModTime().UnixNano(), info.Size())
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	select {
	case thumbnailSlots <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	defer func() { <-thumbnailSlots }()
	file, _, err := s.files.OpenRegular(name)
	if err != nil {
		filesystemError(w, err)
		return
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, 415, "无法生成缩略图，请点击文件名预览")
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		writeError(w, 500, "无法读取图片")
		return
	}
	original, _, err := image.Decode(file)
	if err != nil {
		writeError(w, 415, "无法读取图片")
		return
	}
	bounds := original.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > 128 || height > 128 {
		if width >= height {
			height = max(1, height*128/width)
			width = 128
		} else {
			width = max(1, width*128/height)
			height = 128
		}
	}
	small := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			red, green, blue, alpha := original.At(bounds.Min.X+x*bounds.Dx()/width, bounds.Min.Y+y*bounds.Dy()/height).RGBA()
			small.SetRGBA(x, y, color.RGBA{R: uint8((red + 65535 - alpha) >> 8), G: uint8((green + 65535 - alpha) >> 8), B: uint8((blue + 65535 - alpha) >> 8), A: 255})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, small, &jpeg.Options{Quality: 80}); err != nil {
		writeError(w, 500, "无法生成缩略图")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, name+".jpg", info.ModTime(), bytes.NewReader(output.Bytes()))
}
