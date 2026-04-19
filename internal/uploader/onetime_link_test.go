package uploader

import (
	"errors"
	"strings"
	"testing"
)

func TestOneTimeLinkCanOnlyBeConsumedOnce(t *testing.T) {
	service := &Service{
		keyPrefix:    "uploads/",
		oneTimeLinks: newOneTimeLinkStore(),
	}

	link, err := service.CreateOneTimeUploadLink("https://upload.example.com", "media/video.mp4", "video/mp4", 10)
	if err != nil {
		t.Fatalf("CreateOneTimeUploadLink() returned error: %v", err)
	}

	if link.Token == "" {
		t.Fatal("expected token to be generated")
	}

	if !strings.Contains(link.URL, link.Token) {
		t.Fatalf("expected URL to contain token, got %q", link.URL)
	}

	payload, err := service.ConsumeOneTimeUploadLink(link.Token)
	if err != nil {
		t.Fatalf("ConsumeOneTimeUploadLink() first use returned error: %v", err)
	}

	if payload.ObjectKey != "uploads/media/video.mp4" {
		t.Fatalf("expected object key %q, got %q", "uploads/media/video.mp4", payload.ObjectKey)
	}

	_, err = service.ConsumeOneTimeUploadLink(link.Token)
	if !errors.Is(err, ErrOneTimeLinkConsumed) {
		t.Fatalf("expected ErrOneTimeLinkConsumed, got %v", err)
	}
}

func TestOneTimeLinkInvalidExpiry(t *testing.T) {
	service := &Service{
		oneTimeLinks: newOneTimeLinkStore(),
	}

	_, err := service.CreateOneTimeUploadLink("https://upload.example.com", "", "", 2000)
	if !errors.Is(err, ErrInvalidExpiry) {
		t.Fatalf("expected ErrInvalidExpiry, got %v", err)
	}
}
