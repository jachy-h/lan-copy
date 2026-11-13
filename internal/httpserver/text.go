package httpserver

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"lan-copy/internal/textstore"
)

func (s *Server) listTexts(w http.ResponseWriter, _ *http.Request) {
	texts, err := s.texts.List()
	if err != nil {
		log.Printf("异常: 读取共享碎片失败: %v", err)
		writeError(w, http.StatusInternalServerError, "无法读取内容")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"texts": texts, "fragments": texts})
}

func (s *Server) createText(w http.ResponseWriter, r *http.Request) {
	// JSON may escape one input byte into several wire bytes (for example, newlines).
	r.Body = http.MaxBytesReader(w, r.Body, textstore.MaxTextBytes+64<<10)
	var input struct {
		Content string `json:"content"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "文本超过 100,000 字限制")
		} else {
			writeError(w, http.StatusBadRequest, "文本数据格式无效")
		}
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "文本数据格式无效")
		return
	}
	if strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, "请输入要保存的文本")
		return
	}
	if utf8.RuneCountInString(input.Content) > textstore.MaxTextChars || len([]byte(input.Content)) > textstore.MaxTextBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "文本超过 100,000 字限制")
		return
	}

	item, err := s.texts.Create(input.Content)
	if errors.Is(err, textstore.ErrCountLimit) {
		writeError(w, http.StatusRequestEntityTooLarge, "内容数量已达上限")
		return
	}
	if errors.Is(err, textstore.ErrStoreLimit) {
		writeError(w, http.StatusRequestEntityTooLarge, "内容存储已达上限")
		return
	}
	if err != nil {
		log.Printf("异常: 保存共享文本失败: %v", err)
		writeError(w, http.StatusInternalServerError, "保存内容失败")
		return
	}
	s.notifyTextsChanged()
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) uploadFragmentImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, textstore.MaxImageBytes+1<<20)
	if err := r.ParseMultipartForm(textstore.MaxImageBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "图片超过 20 MiB 限制")
		} else {
			writeError(w, http.StatusBadRequest, "图片数据格式无效")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("image")
	if err != nil {
		writeError(w, http.StatusBadRequest, "请选择要上传的图片")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, textstore.MaxImageBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法读取上传的图片")
		return
	}
	if len(data) > textstore.MaxImageBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "图片超过 20 MiB 限制")
		return
	}
	item, err := s.texts.CreateImage(header.Filename, data)
	if writeFragmentError(w, err) {
		return
	}
	s.notifyTextsChanged()
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) createFragment(w http.ResponseWriter, r *http.Request) {
	maxBytes := int64(textstore.MaxImagesPerFragment)*textstore.MaxImageBytes + textstore.MaxTextBytes + 2<<20
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(textstore.MaxImageBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") {
			writeError(w, http.StatusRequestEntityTooLarge, "碎片内容超过大小限制")
		} else {
			writeError(w, http.StatusBadRequest, "碎片数据格式无效")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["images"]
	if len(files) > textstore.MaxImagesPerFragment {
		writeError(w, http.StatusRequestEntityTooLarge, "每条碎片最多 6 张图片")
		return
	}
	uploads := make([]textstore.ImageUpload, 0, len(files))
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "无法读取上传的图片")
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, textstore.MaxImageBytes+1))
		_ = file.Close()
		if readErr != nil {
			writeError(w, http.StatusBadRequest, "无法读取上传的图片")
			return
		}
		if len(data) > textstore.MaxImageBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "单张图片超过 20 MiB 限制")
			return
		}
		uploads = append(uploads, textstore.ImageUpload{Name: header.Filename, Data: data})
	}
	item, err := s.texts.CreateFragment(r.FormValue("content"), uploads)
	if writeFragmentError(w, err) {
		return
	}
	s.notifyTextsChanged()
	writeJSON(w, http.StatusCreated, item)
}

func writeFragmentError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, textstore.ErrEmpty):
		writeError(w, http.StatusBadRequest, "请输入文字或添加图片")
	case errors.Is(err, textstore.ErrTextLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "文本超过 100,000 字限制")
	case errors.Is(err, textstore.ErrUnsupportedImage):
		writeError(w, http.StatusUnsupportedMediaType, "仅支持 PNG、JPEG、GIF、WebP 或 BMP 图片")
	case errors.Is(err, textstore.ErrImageLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "单张图片超过 20 MiB 限制")
	case errors.Is(err, textstore.ErrImageCountLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "每条碎片最多 6 张图片")
	case errors.Is(err, textstore.ErrCountLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "内容数量已达上限")
	case errors.Is(err, textstore.ErrStoreLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "碎片存储已达上限")
	default:
		log.Printf("异常: 保存碎片失败: %v", err)
		writeError(w, http.StatusInternalServerError, "保存内容失败")
	}
	return true
}

func (s *Server) serveFragmentImage(w http.ResponseWriter, r *http.Request) {
	s.serveFragmentImageAt(w, r, 0)
}

func (s *Server) serveFragmentImageAt(w http.ResponseWriter, r *http.Request, index int) {
	if index < 0 {
		writeError(w, http.StatusBadRequest, "图片编号无效")
		return
	}
	id := r.PathValue("id")
	if !validTextID(id) {
		writeError(w, http.StatusBadRequest, "内容编号无效")
		return
	}
	file, item, image, err := s.texts.OpenImageAt(id, index)
	if errors.Is(err, textstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "图片不存在或已到期")
		return
	}
	if err != nil {
		log.Printf("异常: 读取碎片图片失败: %v", err)
		writeError(w, http.StatusInternalServerError, "无法读取图片")
		return
	}
	defer file.Close()
	name := image.Name
	if name == "" {
		name = "图片" + path.Ext(image.File)
	}
	w.Header().Set("Content-Type", image.MIME)
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, item.Created, file)
}

func (s *Server) saveFragment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validTextID(id) {
		writeError(w, http.StatusBadRequest, "内容编号无效")
		return
	}
	var input struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, http.StatusBadRequest, "保存位置数据格式无效")
		return
	}
	item, err := s.texts.Get(id)
	if errors.Is(err, textstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "内容不存在或已到期")
		return
	}
	if err != nil {
		log.Printf("异常: 读取待保存碎片失败: %v", err)
		writeError(w, http.StatusInternalServerError, "无法读取内容")
		return
	}
	type savedFile struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	files := make([]savedFile, 0, 1+len(item.Images))
	baseName := strings.TrimSpace(input.Name)
	if strings.TrimSpace(item.Content) != "" {
		name := baseName
		if name == "" {
			name = fragmentSaveName(item)
		}
		saved, err := s.files.SaveFile(input.Path, name, []byte(item.Content))
		if err != nil {
			filesystemError(w, err)
			return
		}
		files = append(files, savedFile{Path: saved, Name: path.Base(saved)})
	}
	images := item.Images
	for index := range images {
		file, _, opened, err := s.texts.OpenImageAt(id, index)
		if err != nil {
			log.Printf("异常: 读取待保存碎片图片失败: id=%s index=%d 错误=%v", id, index, err)
			writeError(w, http.StatusInternalServerError, "部分内容已保存，但有图片无法读取")
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, textstore.MaxImageBytes+1))
		_ = file.Close()
		if readErr != nil || len(data) > textstore.MaxImageBytes {
			writeError(w, http.StatusInternalServerError, "部分内容已保存，但图片过大或无法读取")
			return
		}
		name := opened.Name
		if name == "" {
			name = "图片" + path.Ext(opened.File)
		}
		if len(images) == 1 && strings.TrimSpace(item.Content) == "" && baseName != "" {
			name = baseName
		}
		saved, err := s.files.SaveFile(input.Path, name, data)
		if err != nil {
			filesystemError(w, err)
			return
		}
		files = append(files, savedFile{Path: saved, Name: path.Base(saved)})
	}
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "这条碎片没有可保存的内容")
		return
	}
	s.notifyFilesChanged()
	response := map[string]any{"files": files}
	response["path"] = files[0].Path
	response["name"] = files[0].Name
	writeJSON(w, http.StatusCreated, response)
}

func fragmentSaveName(item textstore.Fragment) string {
	if item.Kind == "image" {
		if item.Name != "" {
			return item.Name
		}
		return "图片" + path.Ext(item.File)
	}
	return "碎片-" + time.Now().Format("20060102-150405") + ".txt"
}

func (s *Server) deleteText(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validTextID(id) {
		writeError(w, http.StatusBadRequest, "文本编号无效")
		return
	}
	if err := s.texts.Delete(id); err != nil {
		if errors.Is(err, textstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "内容不存在")
			return
		}
		log.Printf("异常: 删除共享碎片失败: %v", err)
		writeError(w, http.StatusInternalServerError, "删除内容失败")
		return
	}
	s.notifyTextsChanged()
	w.WriteHeader(http.StatusNoContent)
}

func validTextID(id string) bool {
	if len(id) != 16 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}
