package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"big-file-service/internal/config"
	appmiddleware "big-file-service/internal/middleware"
	"big-file-service/internal/uploader"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

type Handler struct {
	uploader *uploader.Service
	cfg      *config.Config
	logger   *zap.Logger
}

type presignRequest struct {
	Key              string `json:"key"`
	ContentType      string `json:"content_type"`
	ExpiresInMinutes int    `json:"expires_in_minutes"`
}

type oneTimeLinkRequest struct {
	Key              string `json:"key"`
	ContentType      string `json:"content_type"`
	ExpiresInMinutes int    `json:"expires_in_minutes"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(uploadService *uploader.Service, cfg *config.Config, logger *zap.Logger) *Handler {
	return &Handler{
		uploader: uploadService,
		cfg:      cfg,
		logger:   logger,
	}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(h.requestLogger)

	r.Get("/healthz", h.health)
	r.Put("/upload/one-time/{token}", h.uploadWithOneTimeLink)

	r.Group(func(router chi.Router) {
		router.Use(appmiddleware.NewAuthMiddleware(h.cfg.Security, h.logger))
		router.Post("/upload", h.uploadFile)
		router.Post("/upload/presign", h.presignUpload)
		router.Post("/upload/one-time/link", h.createOneTimeUploadLink)
		router.Get("/upload/{upload_id}/status", h.uploadStatus)
		router.Delete("/upload/{upload_id}", h.abortUpload)
	})

	return r
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) uploadFile(w http.ResponseWriter, r *http.Request) {
	maxBodySize := h.cfg.Upload.MaxFileSizeBytes() + (10 * 1024 * 1024)
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form payload")
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	contentType := strings.TrimSpace(r.FormValue("content_type"))
	if contentType == "" {
		contentType = strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(fileHeader.Filename)))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	size := fileHeader.Size
	if size <= 0 {
		end, seekErr := file.Seek(0, io.SeekEnd)
		if seekErr != nil {
			writeError(w, http.StatusBadRequest, "failed to determine file size")
			return
		}
		size = end
	}
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		writeError(w, http.StatusBadRequest, "failed to seek upload stream")
		return
	}

	if size <= 0 {
		writeError(w, http.StatusBadRequest, "uploaded file is empty")
		return
	}

	if size > h.cfg.Upload.MaxFileSizeBytes() {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds max upload size")
		return
	}

	result, err := h.uploader.Upload(r.Context(), uploader.UploadRequest{
		File:        file,
		Filename:    fileHeader.Filename,
		ContentType: contentType,
		ObjectKey:   strings.TrimSpace(r.FormValue("key")),
		Size:        size,
	})
	if err != nil {
		h.handleUploadError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) presignUpload(w http.ResponseWriter, r *http.Request) {
	var request presignRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.uploader.GeneratePresignedUploadURL(
		r.Context(),
		request.Key,
		request.ContentType,
		request.ExpiresInMinutes,
	)
	if err != nil {
		if errors.Is(err, uploader.ErrObjectKeyRequired) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		h.logger.Error("failed generating presigned upload url", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to generate presigned url")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) createOneTimeUploadLink(w http.ResponseWriter, r *http.Request) {
	var request oneTimeLinkRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.uploader.CreateOneTimeUploadLink(
		requestBaseURL(r),
		request.Key,
		request.ContentType,
		request.ExpiresInMinutes,
	)
	if err != nil {
		switch {
		case errors.Is(err, uploader.ErrInvalidExpiry):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, uploader.ErrObjectKeyRequired):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			h.logger.Error("failed creating one-time upload link", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "failed to create one-time link")
		}
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) uploadWithOneTimeLink(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	link, err := h.uploader.ConsumeOneTimeUploadLink(token)
	if err != nil {
		switch {
		case errors.Is(err, uploader.ErrOneTimeLinkNotFound):
			writeError(w, http.StatusNotFound, "one-time link not found")
		case errors.Is(err, uploader.ErrOneTimeLinkExpired):
			writeError(w, http.StatusGone, "one-time link expired")
		case errors.Is(err, uploader.ErrOneTimeLinkConsumed):
			writeError(w, http.StatusConflict, "one-time link already used")
		default:
			h.logger.Error("failed consuming one-time upload link", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "failed to validate one-time link")
		}
		return
	}

	tempFile, size, err := copyBodyToTempFile(r.Body, h.cfg.Upload.MaxFileSizeBytes())
	if err != nil {
		if errors.Is(err, uploader.ErrFileTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
	}()

	contentType := strings.TrimSpace(link.ContentType)
	if contentType == "" {
		contentType = strings.TrimSpace(r.Header.Get("Content-Type"))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	result, err := h.uploader.Upload(r.Context(), uploader.UploadRequest{
		File:        tempFile,
		Filename:    filepath.Base(link.ObjectKey),
		ContentType: contentType,
		ObjectKey:   link.ObjectKey,
		Size:        size,
	})
	if err != nil {
		h.handleUploadError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) uploadStatus(w http.ResponseWriter, r *http.Request) {
	uploadID := strings.TrimSpace(chi.URLParam(r, "upload_id"))
	if uploadID == "" {
		writeError(w, http.StatusBadRequest, "upload_id is required")
		return
	}

	status, exists := h.uploader.GetStatus(uploadID)
	if !exists {
		writeError(w, http.StatusNotFound, "upload not found")
		return
	}

	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) abortUpload(w http.ResponseWriter, r *http.Request) {
	uploadID := strings.TrimSpace(chi.URLParam(r, "upload_id"))
	if uploadID == "" {
		writeError(w, http.StatusBadRequest, "upload_id is required")
		return
	}

	aborted, err := h.uploader.AbortUpload(r.Context(), uploadID)
	if err != nil {
		h.logger.Error("failed aborting upload", zap.String("upload_id", uploadID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to abort upload")
		return
	}

	if !aborted {
		writeError(w, http.StatusConflict, "upload is not in progress")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"upload_id": uploadID,
		"aborted":   true,
	})
}

func (h *Handler) handleUploadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, uploader.ErrFileRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, uploader.ErrObjectKeyRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, uploader.ErrFileTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, context.Canceled):
		writeError(w, http.StatusRequestTimeout, "upload canceled")
	default:
		h.logger.Error("upload failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "upload failed")
	}
}

func (h *Handler) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		startedAt := time.Now()

		next.ServeHTTP(ww, r)

		h.logger.Info("http request",
			zap.String("request_id", chimw.GetReqID(r.Context())),
			zap.String("method", r.Method),
			zap.String("path", sanitizePathForLog(r.URL.Path)),
			zap.Int("status", ww.Status()),
			zap.Int("bytes", ww.BytesWritten()),
			zap.Duration("duration", time.Since(startedAt)),
		)
	})
}

func copyBodyToTempFile(body io.ReadCloser, maxBytes int64) (*os.File, int64, error) {
	if body == nil {
		return nil, 0, fmt.Errorf("request body is required")
	}
	defer body.Close()

	tempFile, err := os.CreateTemp("", "onetime-upload-*")
	if err != nil {
		return nil, 0, fmt.Errorf("create temporary file: %w", err)
	}

	written, err := io.Copy(tempFile, io.LimitReader(body, maxBytes+1))
	if err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return nil, 0, fmt.Errorf("read request body: %w", err)
	}

	if written == 0 {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return nil, 0, fmt.Errorf("request body is empty")
	}

	if written > maxBytes {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return nil, 0, fmt.Errorf("%w: max %d bytes", uploader.ErrFileTooLarge, maxBytes)
	}

	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
		return nil, 0, fmt.Errorf("seek temporary file: %w", err)
	}

	return tempFile, written, nil
}

func sanitizePathForLog(path string) string {
	if strings.HasPrefix(path, "/upload/one-time/") {
		return "/upload/one-time/{token}"
	}
	return path
}

func requestBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}

	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}

	if host == "" {
		return ""
	}

	return proto + "://" + host
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
