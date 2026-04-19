package uploader

import (
	"context"
	"sync"
	"time"
)

type UploadState string

const (
	StatusPending    UploadState = "pending"
	StatusInProgress UploadState = "in_progress"
	StatusCompleted  UploadState = "completed"
	StatusFailed     UploadState = "failed"
)

type UploadStatus struct {
	UploadID        string      `json:"upload_id"`
	Status          UploadState `json:"status"`
	ProgressPercent float64     `json:"progress_percent"`
	ChunksDone      int32       `json:"chunks_done"`
	ChunksTotal     int32       `json:"chunks_total"`
	BytesUploaded   int64       `json:"bytes_uploaded"`
	BytesTotal      int64       `json:"bytes_total"`
	Strategy        string      `json:"strategy,omitempty"`
	StartedAt       time.Time   `json:"started_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
	Error           string      `json:"error,omitempty"`
}

type uploadControl struct {
	cancel     context.CancelFunc
	s3UploadID string
	objectKey  string
}

type Tracker struct {
	mu       sync.RWMutex
	statuses map[string]*UploadStatus
	controls map[string]uploadControl
}

func NewTracker() *Tracker {
	return &Tracker{
		statuses: make(map[string]*UploadStatus),
		controls: make(map[string]uploadControl),
	}
}

func (t *Tracker) Create(uploadID, strategy string, totalBytes int64, chunksTotal int32) {
	now := time.Now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.statuses[uploadID] = &UploadStatus{
		UploadID:        uploadID,
		Status:          StatusPending,
		ProgressPercent: 0,
		ChunksDone:      0,
		ChunksTotal:     chunksTotal,
		BytesUploaded:   0,
		BytesTotal:      totalBytes,
		Strategy:        strategy,
		StartedAt:       now,
		UpdatedAt:       now,
	}
}

func (t *Tracker) SetInProgress(uploadID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if status, exists := t.statuses[uploadID]; exists {
		status.Status = StatusInProgress
		status.UpdatedAt = time.Now().UTC()
	}
}

func (t *Tracker) Update(uploadID string, chunksDone int32, bytesUploaded int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	status, exists := t.statuses[uploadID]
	if !exists {
		return
	}

	status.Status = StatusInProgress
	status.ChunksDone = chunksDone
	status.BytesUploaded = bytesUploaded
	status.ProgressPercent = calculateProgress(bytesUploaded, status.BytesTotal)
	status.UpdatedAt = time.Now().UTC()
}

func (t *Tracker) Complete(uploadID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	status, exists := t.statuses[uploadID]
	if !exists {
		return
	}

	status.Status = StatusCompleted
	status.ChunksDone = status.ChunksTotal
	status.BytesUploaded = status.BytesTotal
	status.ProgressPercent = 100
	status.UpdatedAt = time.Now().UTC()
	status.Error = ""
}

func (t *Tracker) Fail(uploadID, errMessage string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	status, exists := t.statuses[uploadID]
	if !exists {
		return
	}

	status.Status = StatusFailed
	status.UpdatedAt = time.Now().UTC()
	status.Error = errMessage
}

func (t *Tracker) Get(uploadID string) (UploadStatus, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	status, exists := t.statuses[uploadID]
	if !exists {
		return UploadStatus{}, false
	}

	return *status, true
}

func (t *Tracker) SetControl(uploadID string, cancel context.CancelFunc, s3UploadID, objectKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.controls[uploadID] = uploadControl{
		cancel:     cancel,
		s3UploadID: s3UploadID,
		objectKey:  objectKey,
	}
}

func (t *Tracker) UpdateMultipartControl(uploadID, s3UploadID, objectKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	control, exists := t.controls[uploadID]
	if !exists {
		return
	}

	if s3UploadID != "" {
		control.s3UploadID = s3UploadID
	}
	if objectKey != "" {
		control.objectKey = objectKey
	}

	t.controls[uploadID] = control
}

func (t *Tracker) GetControl(uploadID string) (uploadControl, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	control, exists := t.controls[uploadID]
	if !exists {
		return uploadControl{}, false
	}

	return control, true
}

func (t *Tracker) DeleteControl(uploadID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.controls, uploadID)
}

func calculateProgress(uploaded, total int64) float64 {
	if total <= 0 {
		return 0
	}

	progress := (float64(uploaded) / float64(total)) * 100
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}
