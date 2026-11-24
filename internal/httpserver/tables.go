package httpserver

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"lan-copy/internal/tablestore"
)

const (
	tableAction      = "tables"
	maxTableJSONBody = 2 << 20
)

type tableQueryRequest struct {
	Path string `json:"path"`
	tablestore.Query
}

type tableUpdateRequest struct {
	Path string `json:"path"`
	tablestore.UpdateRequest
}

type tableCreateRequest struct {
	Path    string   `json:"path"`
	Columns []string `json:"columns"`
}

type tableMarkRequest struct {
	Path   string `json:"path"`
	Marked *bool  `json:"marked"`
}

type tableLayoutRequest struct {
	Path string `json:"path"`
	tablestore.Layout
}

type tableQueryResponse struct {
	Path     string             `json:"path"`
	Version  string             `json:"version"`
	Columns  []string           `json:"columns"`
	Rows     []tablestore.Row   `json:"rows"`
	Total    int                `json:"total"`
	Offset   int                `json:"offset"`
	Limit    int                `json:"limit"`
	Modified time.Time          `json:"modified"`
	Layout   *tablestore.Layout `json:"layout,omitempty"`
}

func (s *Server) requireTables(w http.ResponseWriter, r *http.Request) bool {
	if s.tables == nil {
		writeError(w, http.StatusServiceUnavailable, "智能表格不可用")
		return false
	}
	return true
}

func (s *Server) requireTableWrite(w http.ResponseWriter, r *http.Request) bool {
	if !s.requireTables(w, r) {
		return false
	}
	if !validActionRequest(r, tableAction) {
		writeError(w, http.StatusForbidden, "请求来源校验失败")
		return false
	}
	return true
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	if !s.requireTables(w, r) {
		return
	}
	tables, err := s.tables.List(r.Context())
	if err != nil {
		tableHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
}

func (s *Server) queryTable(w http.ResponseWriter, r *http.Request) {
	if !s.requireTables(w, r) {
		return
	}
	var request tableQueryRequest
	if err := decodeTableJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "表格查询参数无效")
		return
	}
	s.queryTableCore(w, r, request, true)
}

func (s *Server) queryTableCore(w http.ResponseWriter, r *http.Request, request tableQueryRequest, withLayout bool) {
	table, result, err := s.tables.Query(r.Context(), request.Path, request.Query)
	if err != nil {
		tableHTTPError(w, err)
		return
	}
	response := tableQueryResponse{
		Path: request.Path, Version: table.Version,
		Columns: result.Columns, Rows: result.Rows, Total: result.Total,
		Offset: result.Offset, Limit: result.Limit, Modified: table.Modified,
	}
	if withLayout {
		layout, _ := s.tables.ReadLayout(request.Path)
		normalized := tablestore.NormalizeLayout(layout, result.Columns)
		response.Layout = &normalized
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) saveTableLayout(w http.ResponseWriter, r *http.Request) {
	if !s.requireTableWrite(w, r) {
		return
	}
	var request tableLayoutRequest
	if err := decodeTableJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "表格列偏好参数无效")
		return
	}
	layout, err := s.tables.SaveLayout(r.Context(), request.Path, request.Layout)
	if err != nil {
		tableHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": request.Path, "layout": layout})
}

func (s *Server) setTableMark(w http.ResponseWriter, r *http.Request) {
	if !s.requireTableWrite(w, r) {
		return
	}
	var request tableMarkRequest
	if err := decodeTableJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "表格标记参数无效")
		return
	}
	if request.Marked == nil {
		writeError(w, http.StatusBadRequest, "表格标记参数无效")
		return
	}
	if err := s.tables.SetMark(r.Context(), request.Path, *request.Marked); err != nil {
		tableHTTPError(w, err)
		return
	}
	s.notifyFilesChanged()
	writeJSON(w, http.StatusOK, map[string]any{"path": request.Path, "marked": *request.Marked})
}

func (s *Server) createTable(w http.ResponseWriter, r *http.Request) {
	if !s.requireTableWrite(w, r) {
		return
	}
	var request tableCreateRequest
	if err := decodeTableJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "新建表格参数无效")
		return
	}
	table, err := s.tables.Create(r.Context(), request.Path, request.Columns)
	if err != nil {
		tableHTTPError(w, err)
		return
	}
	s.notifyFilesChanged()
	writeJSON(w, http.StatusCreated, map[string]any{
		"path": table.Path, "version": table.Version,
		"columns": table.Columns, "modified": table.Modified,
	})
}

func (s *Server) lockAndUpdateTable(w http.ResponseWriter, r *http.Request) {
	if !s.requireTableWrite(w, r) {
		return
	}
	var request tableUpdateRequest
	if err := decodeTableJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "表格更新参数无效")
		return
	}
	s.lockAndUpdateTableCore(w, r, request, "")
}

func (s *Server) lockAndUpdateTableCore(w http.ResponseWriter, r *http.Request, request tableUpdateRequest, pluginID string) {
	version, applied, err := s.tables.LockAndUpdate(r.Context(), request.Path, request.UpdateRequest)
	if err != nil {
		if errors.Is(err, tablestore.ErrVersionConflict) {
			current, _ := s.tables.CurrentVersion(request.Path)
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "表格已被其他设备或插件修改，请重新加载后再保存",
				"code":  "version_conflict", "version": current,
			})
			return
		}
		tableHTTPError(w, err)
		return
	}
	s.notifyFilesChanged()
	if pluginID != "" {
		log.Printf("插件 %s 更新智能表格: path=%s inserted=%d updated=%d deleted=%d",
			pluginID, request.Path, applied.Inserted, applied.Updated, applied.Deleted)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": request.Path, "version": version, "applied": applied,
	})
}

func (s *Server) requirePluginAPI(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.plugins == nil {
		writeError(w, http.StatusServiceUnavailable, "插件 API 不可用")
		return "", false
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "缺少插件 API 令牌")
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	pluginID, ok := s.plugins.AuthenticateAPIToken(token)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "插件 API 令牌无效或实例未运行")
		return "", false
	}
	return pluginID, true
}

func (s *Server) pluginAPITableList(w http.ResponseWriter, r *http.Request) {
	if !s.requireTables(w, r) {
		return
	}
	if _, ok := s.requirePluginAPI(w, r); !ok {
		return
	}
	tables, err := s.tables.List(r.Context())
	if err != nil {
		tableHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
}

func (s *Server) pluginAPITableQuery(w http.ResponseWriter, r *http.Request) {
	if !s.requireTables(w, r) {
		return
	}
	if _, ok := s.requirePluginAPI(w, r); !ok {
		return
	}
	var request tableQueryRequest
	if err := decodeTableJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "表格查询参数无效")
		return
	}
	s.queryTableCore(w, r, request, false)
}

func (s *Server) pluginAPITableUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireTables(w, r) {
		return
	}
	pluginID, ok := s.requirePluginAPI(w, r)
	if !ok {
		return
	}
	var request tableUpdateRequest
	if err := decodeTableJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "表格更新参数无效")
		return
	}
	s.lockAndUpdateTableCore(w, r, request, pluginID)
}

func decodeTableJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxTableJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEnd(decoder)
}

func tableHTTPError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytes):
		writeError(w, http.StatusRequestEntityTooLarge, "表格请求过大")
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "表格不存在")
	case errors.Is(err, fs.ErrExist):
		writeError(w, http.StatusConflict, "同名表格已存在")
	case errors.Is(err, fs.ErrPermission), errors.Is(err, fs.ErrInvalid):
		writeError(w, http.StatusBadRequest, "表格路径无效")
	case errors.Is(err, tablestore.ErrNotCSV), errors.Is(err, tablestore.ErrEmptyTable),
		errors.Is(err, tablestore.ErrInvalidCSV), errors.Is(err, tablestore.ErrInvalidQuery),
		errors.Is(err, tablestore.ErrUnknownColumn), errors.Is(err, tablestore.ErrKeyRequired),
		errors.Is(err, tablestore.ErrKeyMissing), errors.Is(err, tablestore.ErrKeyChange):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, tablestore.ErrTooLarge), errors.Is(err, tablestore.ErrTooManyRows),
		errors.Is(err, tablestore.ErrTooManyColumns), errors.Is(err, tablestore.ErrCellTooLarge),
		errors.Is(err, tablestore.ErrTooManyOperations):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, tablestore.ErrKeyDuplicate), errors.Is(err, tablestore.ErrKeyNotFound),
		errors.Is(err, tablestore.ErrVersionConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "无法完成表格操作")
	}
}
