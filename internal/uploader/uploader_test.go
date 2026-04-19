package uploader

import "testing"

func TestTrackerLifecycle(t *testing.T) {
	tracker := NewTracker()
	uploadID := "upload-test"

	tracker.Create(uploadID, "multipart", 100, 4)
	tracker.SetInProgress(uploadID)
	tracker.Update(uploadID, 2, 50)

	status, exists := tracker.Get(uploadID)
	if !exists {
		t.Fatal("expected upload status to exist")
	}

	if status.Status != StatusInProgress {
		t.Fatalf("expected status in_progress, got %s", status.Status)
	}

	if status.ChunksDone != 2 {
		t.Fatalf("expected chunks_done 2, got %d", status.ChunksDone)
	}

	if status.ProgressPercent != 50 {
		t.Fatalf("expected progress 50, got %.2f", status.ProgressPercent)
	}

	tracker.Complete(uploadID)
	status, _ = tracker.Get(uploadID)

	if status.Status != StatusCompleted {
		t.Fatalf("expected status completed, got %s", status.Status)
	}

	if status.ProgressPercent != 100 {
		t.Fatalf("expected progress 100, got %.2f", status.ProgressPercent)
	}
}

func TestNormalizeObjectKey(t *testing.T) {
	service := &Service{keyPrefix: "uploads/"}

	tests := []struct {
		name      string
		customKey string
		filename  string
		want      string
	}{
		{name: "custom key with prefix", customKey: "images/a.jpg", filename: "a.jpg", want: "uploads/images/a.jpg"},
		{name: "fallback filename", customKey: "", filename: "a.jpg", want: "uploads/a.jpg"},
		{name: "strip leading slash", customKey: "/logs/app.log", filename: "app.log", want: "uploads/logs/app.log"},
		{name: "empty result", customKey: "", filename: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.normalizeObjectKey(tt.customKey, tt.filename)
			if got != tt.want {
				t.Fatalf("normalizeObjectKey() = %q, want %q", got, tt.want)
			}
		})
	}
}
