package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"big-file-service/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

type contextKey string

const AuthTypeContextKey contextKey = "auth_type"

type authenticator struct {
	apiKey    string
	jwtSecret []byte
	logger    *zap.Logger
}

func NewAuthMiddleware(cfg config.SecurityConfig, logger *zap.Logger) func(http.Handler) http.Handler {
	return (&authenticator{
		apiKey:    strings.TrimSpace(cfg.APIKey),
		jwtSecret: []byte(strings.TrimSpace(cfg.JWTSecret)),
		logger:    logger,
	}).Middleware
}

func (a *authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := extractBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			writeUnauthorized(w)
			return
		}

		authType, err := a.validateToken(token)
		if err != nil {
			a.logger.Warn("authentication failed", zap.Error(err))
			writeUnauthorized(w)
			return
		}

		ctx := context.WithValue(r.Context(), AuthTypeContextKey, authType)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *authenticator) validateToken(token string) (string, error) {
	if a.apiKey != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.apiKey)) == 1 {
		return "api_key", nil
	}

	if len(a.jwtSecret) > 0 {
		parsedToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected jwt signing method")
			}
			return a.jwtSecret, nil
		})
		if err == nil && parsedToken != nil && parsedToken.Valid {
			return "jwt", nil
		}
	}

	return "", errors.New("invalid credentials")
}

func extractBearerToken(authHeader string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(authHeader), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("invalid authorization header")
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errors.New("empty bearer token")
	}

	return token, nil
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
