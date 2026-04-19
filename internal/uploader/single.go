package uploader

import (
	"context"
	"fmt"
	"io"
	"strings"

	retry "github.com/avast/retry-go"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

func (s *Service) uploadSingle(ctx context.Context, uploadID, objectKey, contentType string, file io.ReadSeeker, size int64) (string, error) {
	resolvedContentType := strings.TrimSpace(contentType)
	if resolvedContentType == "" {
		resolvedContentType = "application/octet-stream"
	}

	var output *s3.PutObjectOutput
	err := retry.Do(
		func() error {
			if err := ctx.Err(); err != nil {
				return retry.Unrecoverable(err)
			}

			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return retry.Unrecoverable(fmt.Errorf("seek upload file: %w", err))
			}

			input := &s3.PutObjectInput{
				Bucket:      aws.String(s.bucket),
				Key:         aws.String(objectKey),
				Body:        file,
				ContentType: aws.String(resolvedContentType),
			}

			if storageClass := s.storageClassType(); storageClass != "" {
				input.StorageClass = storageClass
			}

			result, err := s.s3.PutObject(ctx, input)
			if err != nil {
				return err
			}

			output = result
			return nil
		},
		retry.Attempts(s.cfg.RetryAttempts),
		retry.Delay(s.cfg.RetryDelay()),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
		retry.OnRetry(func(attempt uint, err error) {
			s.logger.Warn("single upload retry",
				zap.String("upload_id", uploadID),
				zap.Uint("attempt", attempt+1),
				zap.Error(err),
			)
		}),
	)
	if err != nil {
		return "", fmt.Errorf("single upload failed: %w", err)
	}

	s.tracker.Update(uploadID, 1, size)
	return strings.Trim(aws.ToString(output.ETag), "\""), nil
}
