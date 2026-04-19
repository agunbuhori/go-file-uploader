# 📦 go-s3-uploader

A production-ready Go service for uploading large files to Amazon S3 using **multipart (chunked) upload** and **single upload**, with structured logging, robust error handling, automatic retries, and upload failure management.

---

## ✨ Features

| Feature | Description |
|---|---|
| Multipart Upload | Large files are split into chunks and uploaded in parallel |
| Single Upload | Small files sent in a single request |
| Auto-detect Mode | Automatically selects upload strategy based on file size |
| Retry + Backoff | Failed uploads are retried with exponential backoff |
| Presigned URL | Generate temporary URLs for download/upload without exposing credentials |
| One-Time Upload Link | Generate single-use upload links that auto-expire and reject replay |
| Structured Logging | JSON logs via `zap` with request tracing |
| Security | TLS enforcement, credential isolation via `.env`, IAM-scoped access |
| Progress Tracking | Real-time upload progress per chunk |
| Graceful Shutdown | In-flight uploads complete before the process exits |

---

## 🗂️ Project Structure

```
go-s3-uploader/
├── cmd/
│   └── server/
│       └── main.go             # Entry point
├── internal/
│   ├── config/
│   │   └── config.go           # .env loader and config struct
│   ├── uploader/
│   │   ├── uploader.go         # Upload orchestrator (auto-detect strategy)
│   │   ├── multipart.go        # Multipart/chunked upload logic
│   │   ├── single.go           # Single file upload logic
│   │   └── progress.go         # Progress tracking
│   ├── s3client/
│   │   └── client.go           # AWS S3 client factory with security config
│   ├── middleware/
│   │   └── auth.go             # API key / JWT middleware
│   └── logger/
│       └── logger.go           # Zap logger setup
├── api/
│   └── handler.go              # HTTP handlers
├── .env.example                # Environment variable template
├── .gitignore
├── go.mod
├── go.sum
└── README.md
```

---

## 🔧 Tech Stack

| Library | Purpose |
|---|---|
| [`aws/aws-sdk-go-v2`](https://github.com/aws/aws-sdk-go-v2) | Official AWS SDK — S3 client, multipart upload |
| [`joho/godotenv`](https://github.com/joho/godotenv) | Load `.env` file into environment |
| [`uber-go/zap`](https://github.com/uber-go/zap) | High-performance structured logging |
| [`avast/retry-go`](https://github.com/avast/retry-go) | Retry with exponential backoff |
| [`go-chi/chi`](https://github.com/go-chi/chi) | Lightweight HTTP router |
| [`golang-jwt/jwt`](https://github.com/golang-jwt/jwt) | JWT authentication middleware |

---

## ⚙️ Configuration

Copy the example env file and fill in your values:

```bash
cp .env.example .env
```

### `.env.example`

```dotenv
# ── AWS Credentials ─────────────────────────────────────────────
AWS_REGION=us-east-1
AWS_ACCESS_KEY_ID=your-access-key-id
AWS_SECRET_ACCESS_KEY=your-secret-access-key
AWS_SESSION_TOKEN=                        # Optional — for temporary credentials

# ── S3 Configuration ────────────────────────────────────────────
S3_BUCKET=your-bucket-name
S3_KEY_PREFIX=uploads/                    # Optional path prefix for all objects
S3_STORAGE_CLASS=STANDARD                 # STANDARD | STANDARD_IA | GLACIER | etc.
S3_ENDPOINT_URL=                          # Optional — for S3-compatible storage (MinIO, etc.)
S3_FORCE_PATH_STYLE=false                 # Set true for MinIO / localstack

# ── Upload Settings ─────────────────────────────────────────────
UPLOAD_CHUNK_SIZE_MB=10                   # Size of each chunk in MB (min: 5 MB per S3 spec)
UPLOAD_CONCURRENCY=5                      # Number of parallel chunk uploads
UPLOAD_MULTIPART_THRESHOLD_MB=20          # Files above this size use multipart upload
UPLOAD_MAX_FILE_SIZE_MB=5120              # Global max file size limit (5 GB)
UPLOAD_RETRY_ATTEMPTS=3                   # Max retry attempts per chunk
UPLOAD_RETRY_DELAY_MS=500                 # Initial retry delay in milliseconds

# ── Server ──────────────────────────────────────────────────────
SERVER_PORT=8080
SERVER_READ_TIMEOUT_SEC=30
SERVER_WRITE_TIMEOUT_SEC=120              # Allow time for large uploads to complete
SERVER_SHUTDOWN_TIMEOUT_SEC=30

# ── Security ────────────────────────────────────────────────────
API_KEY=your-secure-api-key               # Used for simple API key auth
JWT_SECRET=your-jwt-secret               # Used for JWT bearer token auth
TLS_ENABLED=false                         # Set true for HTTPS
TLS_CERT_PATH=./certs/server.crt
TLS_KEY_PATH=./certs/server.key

# ── Logging ─────────────────────────────────────────────────────
LOG_LEVEL=info                            # debug | info | warn | error
LOG_FORMAT=json                           # json | console
```

> **Security note:** Never commit `.env` to version control. It is already listed in `.gitignore`.

---

## 🚀 Getting Started

### Prerequisites

- Go `>= 1.23`
- An AWS account with S3 access, or a local S3-compatible service (e.g. [MinIO](https://min.io/))

### Install dependencies

```bash
go mod download
```

### Run the server

```bash
go run ./cmd/server
```

### Build for production

```bash
go build -o bin/go-s3-uploader ./cmd/server
./bin/go-s3-uploader
```

---

## 📡 API Reference

Management endpoints require authentication. Pass the API key as a header:

```
Authorization: Bearer <your-api-key>
```

One-time upload links are token-authenticated and intentionally do **not** require API key/JWT.

---

### `POST /upload` — Upload a file

Automatically selects single or multipart upload based on file size.

**Request** — `multipart/form-data`

| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | ✅ | The file to upload |
| `key` | string | ❌ | Custom S3 object key (defaults to original filename) |
| `content_type` | string | ❌ | Override MIME type |

**Example**

```bash
curl -X POST http://localhost:8080/upload \
  -H "Authorization: Bearer your-api-key" \
  -F "file=@/path/to/large-file.zip" \
  -F "key=backups/large-file.zip"
```

**Response `200 OK`**

```json
{
  "success": true,
  "key": "backups/large-file.zip",
  "bucket": "your-bucket-name",
  "etag": "\"abc123def456\"",
  "size_bytes": 1073741824,
  "upload_id": "upload_01HXYZ...",
  "strategy": "multipart",
  "chunks": 102,
  "duration_ms": 4821
}
```

---

### `POST /upload/presign` — Generate a presigned upload URL

Returns a time-limited URL the client can use to upload directly to S3 — no server proxy needed.

**Request body** — `application/json`

```json
{
  "key": "uploads/video.mp4",
  "content_type": "video/mp4",
  "expires_in_minutes": 15
}
```

**Response `200 OK`**

```json
{
  "url": "https://your-bucket.s3.amazonaws.com/uploads/video.mp4?X-Amz-...",
  "key": "uploads/video.mp4",
  "expires_at": "2024-11-01T12:15:00Z"
}
```

---

### `POST /upload/one-time/link` — Generate one-time upload link

Creates a single-use upload URL that can be used exactly once. The token is consumed on first request attempt and cannot be reused.

**Request body** — `application/json`

```json
{
  "key": "uploads/onetime/video.mp4",
  "content_type": "video/mp4",
  "expires_in_minutes": 15
}
```

**Response `200 OK`**

```json
{
  "token": "G2JxqVQ6x...",
  "url": "http://localhost:8080/upload/one-time/G2JxqVQ6x...",
  "key": "uploads/onetime/video.mp4",
  "expires_at": "2024-11-01T12:15:00Z"
}
```

**Errors**

- `400` invalid request or expiry out of range (`1..1440` minutes)
- `401` unauthorized management request

---

### `PUT /upload/one-time/:token` — Upload using one-time link

Uploads binary content through a single-use token URL. This endpoint does **not** require API key/JWT.

```bash
curl -X PUT "http://localhost:8080/upload/one-time/G2JxqVQ6x..." \
  -H "Content-Type: video/mp4" \
  --data-binary "@/path/to/video.mp4"
```

**Response `200 OK`**

```json
{
  "success": true,
  "key": "uploads/onetime/video.mp4",
  "bucket": "your-bucket-name",
  "etag": "abc123def456",
  "size_bytes": 104857600,
  "upload_id": "upload_01HXYZ...",
  "strategy": "multipart",
  "chunks": 10,
  "duration_ms": 4120
}
```

**Errors**

- `404` token not found
- `410` token expired
- `409` token already used
- `413` file exceeds configured max upload size

---

### `GET /upload/:upload_id/status` — Check upload status

```bash
curl http://localhost:8080/upload/upload_01HXYZ.../status \
  -H "Authorization: Bearer your-api-key"
```

**Response `200 OK`**

```json
{
  "upload_id": "upload_01HXYZ...",
  "status": "in_progress",
  "progress_percent": 63.5,
  "chunks_done": 65,
  "chunks_total": 102,
  "bytes_uploaded": 681574400,
  "bytes_total": 1073741824
}
```

Possible `status` values: `pending` | `in_progress` | `completed` | `failed`

---

### `DELETE /upload/:upload_id` — Abort a multipart upload

Cancels an in-progress multipart upload and cleans up all uploaded parts on S3.

```bash
curl -X DELETE http://localhost:8080/upload/upload_01HXYZ... \
  -H "Authorization: Bearer your-api-key"
```

**Response `200 OK`**

```json
{
  "upload_id": "upload_01HXYZ...",
  "aborted": true
}
```

---

## 🔐 Security

### Credential isolation

AWS credentials are **never hardcoded**. They are loaded exclusively from `.env` / environment variables via `godotenv`, and never logged or exposed in API responses.

### IAM Least Privilege

Create a dedicated IAM user or role for this service with the minimum required permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowS3Upload",
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:GetObject",
        "s3:DeleteObject",
        "s3:AbortMultipartUpload",
        "s3:ListMultipartUploadParts"
      ],
      "Resource": "arn:aws:s3:::your-bucket-name/*"
    }
  ]
}
```

### Transport Security

- All S3 SDK calls enforce **HTTPS/TLS** by default via `aws-sdk-go-v2`.
- The HTTP server supports TLS (`TLS_ENABLED=true`) — use a valid certificate in production.
- File size and content type are validated before upload begins.

### API Authentication

Two strategies are supported — configure one:

| Strategy | Header | Config Key |
|---|---|---|
| API Key | `Authorization: Bearer <key>` | `API_KEY` |
| JWT | `Authorization: Bearer <token>` | `JWT_SECRET` |

---

## 🔁 Retry & Failure Management

Each chunk is retried independently. On failure, the chunk is retried up to `UPLOAD_RETRY_ATTEMPTS` times using **exponential backoff** starting from `UPLOAD_RETRY_DELAY_MS`.

If all retries are exhausted for any chunk, the entire multipart upload is **automatically aborted** — preventing orphaned parts from accumulating on S3 (which would incur charges).

```
Chunk 42 failed  →  retry 1 (500ms)  →  retry 2 (1s)  →  retry 3 (2s)  →  abort upload
```

On server shutdown (`SIGINT` / `SIGTERM`), in-flight uploads are given `SERVER_SHUTDOWN_TIMEOUT_SEC` seconds to complete before the process exits.

---

## 📋 Logging

All logs are emitted as structured JSON (configurable to console format for local dev) using `zap`.

```json
{
  "level": "info",
  "ts": "2024-11-01T08:00:00.000Z",
  "caller": "uploader/multipart.go:87",
  "msg": "chunk uploaded",
  "upload_id": "upload_01HXYZ...",
  "chunk": 42,
  "chunk_size_bytes": 10485760,
  "duration_ms": 312,
  "attempt": 1
}
```

Error logs always include the full error chain and upload context:

```json
{
  "level": "error",
  "msg": "chunk upload failed, retrying",
  "upload_id": "upload_01HXYZ...",
  "chunk": 42,
  "attempt": 2,
  "error": "RequestError: send request failed: EOF"
}
```

---

## 🧪 Running Tests

```bash
# Unit tests
go test ./...

# With coverage report
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Integration tests (requires AWS credentials or localstack)
go test ./... -tags=integration
```

### Local S3 with LocalStack

```bash
# Start LocalStack
docker run --rm -p 4566:4566 localstack/localstack

# Point the service to LocalStack via .env
S3_ENDPOINT_URL=http://localhost:4566
S3_FORCE_PATH_STYLE=true
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test
```

---

## 📄 License

MIT — see [LICENSE](LICENSE) for details.