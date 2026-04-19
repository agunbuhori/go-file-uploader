package uploader

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultOneTimeLinkExpiry = 15 * time.Minute
	maxOneTimeLinkExpiry     = 24 * time.Hour
)

type OneTimeLinkResult struct {
	Token     string    `json:"token"`
	URL       string    `json:"url"`
	Key       string    `json:"key"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OneTimeLinkPayload struct {
	Token       string
	ObjectKey   string
	ContentType string
	ExpiresAt   time.Time
}

type oneTimeUploadLink struct {
	token       string
	objectKey   string
	contentType string
	expiresAt   time.Time
	consumedAt  time.Time
	consumed    bool
}

type oneTimeLinkStore struct {
	mu    sync.Mutex
	links map[string]*oneTimeUploadLink
}

func newOneTimeLinkStore() *oneTimeLinkStore {
	return &oneTimeLinkStore{
		links: make(map[string]*oneTimeUploadLink),
	}
}

func (s *oneTimeLinkStore) create(objectKey, contentType string, expiresAt time.Time) (string, error) {
	token, err := generateSecureToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.cleanupLocked(now)

	s.links[token] = &oneTimeUploadLink{
		token:       token,
		objectKey:   objectKey,
		contentType: strings.TrimSpace(contentType),
		expiresAt:   expiresAt.UTC(),
	}

	return token, nil
}

func (s *oneTimeLinkStore) consume(token string, now time.Time) (OneTimeLinkPayload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizedToken := strings.TrimSpace(token)
	if normalizedToken == "" {
		return OneTimeLinkPayload{}, ErrOneTimeLinkNotFound
	}

	link, exists := s.links[normalizedToken]
	if !exists {
		return OneTimeLinkPayload{}, ErrOneTimeLinkNotFound
	}

	now = now.UTC()
	if now.After(link.expiresAt) {
		delete(s.links, normalizedToken)
		return OneTimeLinkPayload{}, ErrOneTimeLinkExpired
	}

	if link.consumed {
		return OneTimeLinkPayload{}, ErrOneTimeLinkConsumed
	}

	link.consumed = true
	link.consumedAt = now

	return OneTimeLinkPayload{
		Token:       link.token,
		ObjectKey:   link.objectKey,
		ContentType: link.contentType,
		ExpiresAt:   link.expiresAt,
	}, nil
}

func (s *oneTimeLinkStore) cleanupLocked(now time.Time) {
	for token, link := range s.links {
		if now.After(link.expiresAt) {
			delete(s.links, token)
			continue
		}

		if link.consumed && now.After(link.consumedAt.Add(2*time.Hour)) {
			delete(s.links, token)
		}
	}
}

func generateSecureToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate one-time token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
