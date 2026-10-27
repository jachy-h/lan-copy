package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"lan-copy/internal/printing"
)

func printError(w http.ResponseWriter, err error) {
	var sizeErr *http.MaxBytesError
	switch {
	case errors.Is(err, printing.ErrUnavailable):
		writeError(w, 503, err.Error())
	case errors.Is(err, printing.ErrBusy), errors.Is(err, printing.ErrLayoutChanged):
		writeError(w, 409, err.Error())
	case errors.Is(err, printing.ErrNotFound):
		writeError(w, 404, err.Error())
	case errors.Is(err, printing.ErrTooLarge), errors.As(err, &sizeErr):
		writeError(w, 413, "打印文档超过限制：最多 50 页、单页 12 MiB、总计 128 MiB")
	case errors.Is(err, printing.ErrDocument):
		writeError(w, 400, "打印文档无效或页面不完整，请重新准备预览")
	default:
		writeError(w, 500, "无法处理打印文档："+err.Error())
	}
}

func (s *Server) printLayout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	printer := r.URL.Query().Get("printer")
	if strings.TrimSpace(printer) == "" || len(printer) > 1024 || strings.ContainsRune(printer, 0) {
		writeError(w, 400, "请选择有效的打印机")
		return
	}
	layout, err := s.printing.Layout(printer)
	if err != nil {
		printError(w, err)
		return
	}
	writeJSON(w, 200, layout)
}

func (s *Server) printDocument(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !validPrintingAction(r, "print-document") {
		writeError(w, 403, "请从 Lan Copy 页面发起文件打印")
		return
	}
	select {
	case <-s.appContext.Done():
		writeError(w, 503, "服务正在关闭")
		return
	default:
	}
	r.Body = http.MaxBytesReader(w, r.Body, printing.MaxDocumentBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, 400, "无效的打印上传请求")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		writeError(w, 400, "缺少打印文档信息")
		return
	}
	if part.FormName() != "manifest" || part.FileName() != "" {
		writeError(w, 400, "缺少打印文档信息")
		return
	}
	var manifest struct {
		Printer   string `json:"printer"`
		Name      string `json:"name"`
		Pages     int    `json:"pages"`
		LayoutKey string `json:"layoutKey"`
	}
	metadata, err := io.ReadAll(io.LimitReader(part, 4097))
	if err != nil || len(metadata) > 4096 {
		writeError(w, 400, "打印文档信息过大或不完整")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(metadata))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || ensureJSONEnd(decoder) != nil {
		writeError(w, 400, "无效的打印文档信息")
		return
	}
	if manifest.Printer == "" || len(manifest.Printer) > 1024 || strings.ContainsRune(manifest.Printer, 0) {
		writeError(w, 400, "请选择有效的打印机")
		return
	}
	draft, err := s.printing.BeginDocument(manifest.Printer, manifest.Name, manifest.LayoutKey, manifest.Pages)
	if err != nil {
		printError(w, err)
		return
	}
	defer draft.Abort()
	for i := 0; i < manifest.Pages; i++ {
		page, err := reader.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = printing.ErrDocument
			}
			printError(w, err)
			return
		}
		if page.FormName() != "page" || page.FileName() != fmt.Sprintf("%03d.png", i+1) {
			printError(w, printing.ErrDocument)
			return
		}
		if err := draft.StagePage(page); err != nil {
			printError(w, err)
			return
		}
	}
	if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = printing.ErrDocument
		}
		printError(w, err)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	select {
	case <-s.appContext.Done():
		writeError(w, 503, "服务正在关闭")
		return
	default:
	}
	job, err := draft.Commit()
	if err != nil {
		printError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
