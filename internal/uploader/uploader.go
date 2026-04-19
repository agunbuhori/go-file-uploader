package uploader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"big-file-service/internal/config"
	"big-file-service/internal/s3client"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	ErrFileRequired      = errors.New("file is required")
	ErrObjectKeyRequired = errors.New("object key is required")
	ErrFileTooLarge      = errors.New("file exceeds configured max size")
)

type UploadFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

type UploadRequest struct {
	File        UploadFile
	Filename    string
	ContentType string
	ObjectKey   string
	Size        int64
}

type UploadResult struct {
	Success    bool   `json:"success"`
	Key        string `json:"key"`
	Bucket     string `json:"bucket"`
	ETag       string `json:"etag,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	UploadID   string `json:"upload_id"`
	Strategy   string `json:"strategy"`
	Chunks     int    `json:"chunks"`
	DurationMS int64  `json:"duration_ms"`
}

type PresignResult struct {
	URL       string    `json:"url"`
	Key       string    `json:"key"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Service struct {
	s3           *s3.Client
	presign      *s3.PresignClient
	bucket       string
	keyPrefix    string
	storageClass string
	cfg          config.UploadConfig
	tracker      *Tracker
	logger       *zap.Logger
	wg           sync.WaitGroup
}

func NewService(client *s3client.Client, uploadCfg config.UploadConfig, s3Cfg config.S3Config, logger *zap.Logger) *Service {
	return &Service{
		s3:           client.S3,
		presign:      client.Presign,
		bucket:       client.Bucket,
		keyPrefix:    normalizePrefix(s3Cfg.KeyPrefix),
		storageClass: strings.TrimSpace(s3Cfg.StorageClass),
		cfg:          uploadCfg,
		tracker:      NewTracker(),
		logger:       logger,
	}
}

func (s *Service) Upload(ctx context.Context, request UploadRequest) (UploadResult, error) {
	if request.File == nil {
		return UploadResult{}, ErrFileRequired
	}

	if request.Size <= 0 {
		return UploadResult{}, fmt.Errorf("file size must be > 0")
	}

	if request.Size > s.cfg.MaxFileSizeBytes() {
		return UploadResult{}, fmt.Errorf("%w: got %d bytes, max %d bytes", ErrFileTooLarge, request.Size, s.cfg.MaxFileSizeBytes())
	}

	objectKey := s.normalizeObjectKey(request.ObjectKey, request.Filename)
	if objectKey == "" {
		return UploadResult{}, ErrObjectKeyRequired
	}

	uploadID := uuid.NewString()
	strategy := "single"
	chunksTotal := int32(1)
	if request.Size >= s.cfg.MultipartThresholdBytes() {
		strategy = "multipart"
		chunksTotal = int32(math.Ceil(float64(request.Size) / float64(s.cfg.ChunkSizeBytes())))
	}

	s.tracker.Create(uploadID, strategy, request.Size, chunksTotal)
	uploadCtx, cancel := context.WithCancel(ctx)
	s.tracker.SetControl(uploadID, cancel, "", objectKey)
	defer s.tracker.DeleteControl(uploadID)
	defer cancel()
	s.tracker.SetInProgress(uploadID)

	startedAt := time.Now()
	s.wg.Add(1)
	defer s.wg.Done()

	var (
		eTag string
		err  error
	)

	if strategy == "multipart" {
		eTag, _, err = s.uploadMultipart(uploadCtx, uploadID, objectKey, request.ContentType, request.File, request.Size)
	} else {
		eTag, err = s.uploadSingle(uploadCtx, uploadID, objectKey, request.ContentType, request.File, request.Size)
	}

	if err != nil {
		s.tracker.Fail(uploadID, err.Error())
		return UploadResult{}, err
	}

	s.tracker.Complete(uploadID)

	result := UploadResult{
		Success:    true,
		Key:        objectKey,
		Bucket:     s.bucket,
		ETag:       eTag,
		SizeBytes:  request.Size,
		UploadID:   uploadID,
		Strategy:   strategy,
		Chunks:     int(chunksTotal),
		DurationMS: time.Since(startedAt).Milliseconds(),
	}

	return result, nil
}

func (s *Service) GeneratePresignedUploadURL(ctx context.Context, key, contentType string, expiresInMinutes int) (PresignResult, error) {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return PresignResult{}, ErrObjectKeyRequired
	}

	objectKey := s.normalizeObjectKey(trimmedKey, trimmedKey)
	expires := 15 * time.Minute
	if expiresInMinutes > 0 {
		expires = time.Duration(expiresInMinutes) * time.Minute
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey),
	}

	if strings.TrimSpace(contentType) != "" {
		input.ContentType = aws.String(strings.TrimSpace(contentType))
	}

	if storageClass := s.storageClassType(); storageClass != "" {
		input.StorageClass = storageClass
	}

	presignResult, err := s.presign.PresignPutObject(ctx, input, func(options *s3.PresignOptions) {
		options.Expires = expires
	})
	if err != nil {
		return PresignResult{}, fmt.Errorf("create presigned put object url: %w", err)
	}

	return PresignResult{
		URL:       presignResult.URL,
		Key:       objectKey,
		ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (s *Service) GetStatus(uploadID string) (UploadStatus, bool) {
	return s.tracker.Get(uploadID)
}

func (s *Service) AbortUpload(ctx context.Context, uploadID string) (bool, error) {
	status, exists := s.tracker.Get(uploadID)
	if !exists {
		return false, nil
	}

	if status.Status == StatusCompleted || status.Status == StatusFailed {
		return false, nil
	}

	control, exists := s.tracker.GetControl(uploadID)
	if !exists {
		return false, nil
	}

	if control.cancel != nil {
		control.cancel()
	}

	if control.s3UploadID != "" {
		_, err := s.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.bucket),
			Key:      aws.String(control.objectKey),
			UploadId: aws.String(control.s3UploadID),
		})
		if err != nil {
			return false, fmt.Errorf("abort multipart upload in s3: %w", err)
		}
	}

	s.tracker.Fail(uploadID, "upload aborted by request")
	s.tracker.DeleteControl(uploadID)
	return true, nil
}

func (s *Service) WaitForInFlight(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.wg.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) normalizeObjectKey(customKey, fallbackFilename string) string {
	key := strings.TrimSpace(customKey)
	if key == "" {
		key = strings.TrimSpace(fallbackFilename)
	}
	key = strings.TrimPrefix(key, "/")
	if key == "" {
		return ""
	}
	if s.keyPrefix == "" {
		return key
	}
	if strings.HasPrefix(key, s.keyPrefix) {
		return key
	}
	return s.keyPrefix + key
}

func (s *Service) storageClassType() s3types.StorageClass {
	if strings.TrimSpace(s.storageClass) == "" {
		return ""
	}
	return s3types.StorageClass(strings.ToUpper(strings.TrimSpace(s.storageClass)))
}

func normalizePrefix(prefix string) string {
	trimmed := strings.TrimSpace(prefix)
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return ""
	}
	if !strings.HasSuffix(trimmed, "/") {
		trimmed += "/"
	}
	return trimmed
}
