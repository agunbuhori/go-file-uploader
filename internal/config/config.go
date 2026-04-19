package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const mb = 1024 * 1024

type Config struct {
	AWS      AWSConfig
	S3       S3Config
	Upload   UploadConfig
	Server   ServerConfig
	Security SecurityConfig
	Log      LogConfig
}

type AWSConfig struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type S3Config struct {
	Bucket         string
	KeyPrefix      string
	StorageClass   string
	EndpointURL    string
	ForcePathStyle bool
}

type UploadConfig struct {
	ChunkSizeMB          int64
	Concurrency          int
	MultipartThresholdMB int64
	MaxFileSizeMB        int64
	RetryAttempts        uint
	RetryDelayMS         int64
}

type ServerConfig struct {
	Port            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type SecurityConfig struct {
	APIKey      string
	JWTSecret   string
	TLSEnabled  bool
	TLSCertPath string
	TLSKeyPath  string
}

type LogConfig struct {
	Level  string
	Format string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AWS: AWSConfig{
			Region:          getEnv("AWS_REGION", "us-east-1"),
			AccessKeyID:     strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")),
			SecretAccessKey: strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")),
			SessionToken:    strings.TrimSpace(os.Getenv("AWS_SESSION_TOKEN")),
		},
		S3: S3Config{
			Bucket:         strings.TrimSpace(os.Getenv("S3_BUCKET")),
			KeyPrefix:      strings.TrimSpace(os.Getenv("S3_KEY_PREFIX")),
			StorageClass:   getEnv("S3_STORAGE_CLASS", "STANDARD"),
			EndpointURL:    strings.TrimSpace(os.Getenv("S3_ENDPOINT_URL")),
			ForcePathStyle: getEnvBool("S3_FORCE_PATH_STYLE", false),
		},
		Upload: UploadConfig{
			ChunkSizeMB:          getEnvInt64("UPLOAD_CHUNK_SIZE_MB", 10),
			Concurrency:          getEnvInt("UPLOAD_CONCURRENCY", 5),
			MultipartThresholdMB: getEnvInt64("UPLOAD_MULTIPART_THRESHOLD_MB", 20),
			MaxFileSizeMB:        getEnvInt64("UPLOAD_MAX_FILE_SIZE_MB", 5120),
			RetryAttempts:        uint(getEnvInt("UPLOAD_RETRY_ATTEMPTS", 3)),
			RetryDelayMS:         getEnvInt64("UPLOAD_RETRY_DELAY_MS", 500),
		},
		Server: ServerConfig{
			Port:            getEnv("SERVER_PORT", "8080"),
			ReadTimeout:     time.Duration(getEnvInt("SERVER_READ_TIMEOUT_SEC", 30)) * time.Second,
			WriteTimeout:    time.Duration(getEnvInt("SERVER_WRITE_TIMEOUT_SEC", 120)) * time.Second,
			ShutdownTimeout: time.Duration(getEnvInt("SERVER_SHUTDOWN_TIMEOUT_SEC", 30)) * time.Second,
		},
		Security: SecurityConfig{
			APIKey:      strings.TrimSpace(os.Getenv("API_KEY")),
			JWTSecret:   strings.TrimSpace(os.Getenv("JWT_SECRET")),
			TLSEnabled:  getEnvBool("TLS_ENABLED", false),
			TLSCertPath: strings.TrimSpace(os.Getenv("TLS_CERT_PATH")),
			TLSKeyPath:  strings.TrimSpace(os.Getenv("TLS_KEY_PATH")),
		},
		Log: LogConfig{
			Level:  strings.ToLower(getEnv("LOG_LEVEL", "info")),
			Format: strings.ToLower(getEnv("LOG_FORMAT", "json")),
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	var validationErrors []error

	if strings.TrimSpace(c.AWS.Region) == "" {
		validationErrors = append(validationErrors, errors.New("AWS_REGION is required"))
	}

	if strings.TrimSpace(c.S3.Bucket) == "" {
		validationErrors = append(validationErrors, errors.New("S3_BUCKET is required"))
	}

	if c.Upload.ChunkSizeMB < 5 {
		validationErrors = append(validationErrors, fmt.Errorf("UPLOAD_CHUNK_SIZE_MB must be at least 5 MB, got %d", c.Upload.ChunkSizeMB))
	}

	if c.Upload.Concurrency <= 0 {
		validationErrors = append(validationErrors, fmt.Errorf("UPLOAD_CONCURRENCY must be > 0, got %d", c.Upload.Concurrency))
	}

	if c.Upload.MultipartThresholdMB <= 0 {
		validationErrors = append(validationErrors, fmt.Errorf("UPLOAD_MULTIPART_THRESHOLD_MB must be > 0, got %d", c.Upload.MultipartThresholdMB))
	}

	if c.Upload.MaxFileSizeMB <= 0 {
		validationErrors = append(validationErrors, fmt.Errorf("UPLOAD_MAX_FILE_SIZE_MB must be > 0, got %d", c.Upload.MaxFileSizeMB))
	}

	if c.Upload.MultipartThresholdMB > c.Upload.MaxFileSizeMB {
		validationErrors = append(validationErrors, fmt.Errorf("UPLOAD_MULTIPART_THRESHOLD_MB (%d) cannot be greater than UPLOAD_MAX_FILE_SIZE_MB (%d)", c.Upload.MultipartThresholdMB, c.Upload.MaxFileSizeMB))
	}

	if c.Upload.RetryAttempts == 0 {
		validationErrors = append(validationErrors, errors.New("UPLOAD_RETRY_ATTEMPTS must be > 0"))
	}

	if c.Upload.RetryDelayMS <= 0 {
		validationErrors = append(validationErrors, fmt.Errorf("UPLOAD_RETRY_DELAY_MS must be > 0, got %d", c.Upload.RetryDelayMS))
	}

	if strings.TrimSpace(c.Server.Port) == "" {
		validationErrors = append(validationErrors, errors.New("SERVER_PORT is required"))
	}

	if c.Server.ReadTimeout <= 0 {
		validationErrors = append(validationErrors, errors.New("SERVER_READ_TIMEOUT_SEC must be > 0"))
	}

	if c.Server.WriteTimeout <= 0 {
		validationErrors = append(validationErrors, errors.New("SERVER_WRITE_TIMEOUT_SEC must be > 0"))
	}

	if c.Server.ShutdownTimeout <= 0 {
		validationErrors = append(validationErrors, errors.New("SERVER_SHUTDOWN_TIMEOUT_SEC must be > 0"))
	}

	if strings.TrimSpace(c.Security.APIKey) == "" && strings.TrimSpace(c.Security.JWTSecret) == "" {
		validationErrors = append(validationErrors, errors.New("at least one authentication method must be configured: API_KEY or JWT_SECRET"))
	}

	if c.Security.TLSEnabled {
		if strings.TrimSpace(c.Security.TLSCertPath) == "" {
			validationErrors = append(validationErrors, errors.New("TLS_CERT_PATH is required when TLS_ENABLED=true"))
		}
		if strings.TrimSpace(c.Security.TLSKeyPath) == "" {
			validationErrors = append(validationErrors, errors.New("TLS_KEY_PATH is required when TLS_ENABLED=true"))
		}
	}

	if c.Log.Format != "json" && c.Log.Format != "console" {
		validationErrors = append(validationErrors, fmt.Errorf("LOG_FORMAT must be json or console, got %q", c.Log.Format))
	}

	if len(validationErrors) > 0 {
		return errors.Join(validationErrors...)
	}

	return nil
}

func (u UploadConfig) ChunkSizeBytes() int64 {
	return u.ChunkSizeMB * mb
}

func (u UploadConfig) MultipartThresholdBytes() int64 {
	return u.MultipartThresholdMB * mb
}

func (u UploadConfig) MaxFileSizeBytes() int64 {
	return u.MaxFileSizeMB * mb
}

func (u UploadConfig) RetryDelay() time.Duration {
	return time.Duration(u.RetryDelayMS) * time.Millisecond
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getEnvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
