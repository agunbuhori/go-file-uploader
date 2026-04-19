package uploader

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"

	retry "github.com/avast/retry-go"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.uber.org/zap"
)

func (s *Service) uploadMultipart(ctx context.Context, uploadID, objectKey, contentType string, file io.ReaderAt, size int64) (string, int32, error) {
	resolvedContentType := strings.TrimSpace(contentType)
	if resolvedContentType == "" {
		resolvedContentType = "application/octet-stream"
	}

	createInput := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(objectKey),
		ContentType: aws.String(resolvedContentType),
	}
	if storageClass := s.storageClassType(); storageClass != "" {
		createInput.StorageClass = storageClass
	}

	createResult, err := s.s3.CreateMultipartUpload(ctx, createInput)
	if err != nil {
		return "", 0, fmt.Errorf("create multipart upload: %w", err)
	}

	s3UploadID := aws.ToString(createResult.UploadId)
	if s3UploadID == "" {
		return "", 0, fmt.Errorf("s3 returned empty upload id")
	}
	s.tracker.UpdateMultipartControl(uploadID, s3UploadID, objectKey)

	partSize := s.cfg.ChunkSizeBytes()
	totalParts := int32(math.Ceil(float64(size) / float64(partSize)))
	completedParts := make([]s3types.CompletedPart, totalParts)

	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg            sync.WaitGroup
		once          sync.Once
		firstErr      error
		uploadedBytes atomic.Int64
		uploadedParts atomic.Int32
	)
	sem := make(chan struct{}, s.cfg.Concurrency)

	for partNumber := int32(1); partNumber <= totalParts; partNumber++ {
		offset := int64(partNumber-1) * partSize
		contentLength := minInt64(partSize, size-offset)

		wg.Add(1)
		go func(partNumber int32, offset, contentLength int64) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-uploadCtx.Done():
				return
			}
			defer func() {
				<-sem
			}()

			eTag, partErr := s.uploadPartWithRetry(uploadCtx, uploadID, s3UploadID, objectKey, file, partNumber, offset, contentLength)
			if partErr != nil {
				once.Do(func() {
					firstErr = partErr
					cancel()
				})
				return
			}

			completedParts[partNumber-1] = s3types.CompletedPart{
				ETag:       aws.String(eTag),
				PartNumber: aws.Int32(partNumber),
			}

			totalUploaded := uploadedBytes.Add(contentLength)
			partsDone := uploadedParts.Add(1)
			s.tracker.Update(uploadID, partsDone, totalUploaded)
		}(partNumber, offset, contentLength)
	}

	wg.Wait()

	if firstErr != nil {
		s.abortMultipart(context.Background(), objectKey, s3UploadID, uploadID)
		return "", totalParts, firstErr
	}

	completeResult, err := s.s3.CompleteMultipartUpload(uploadCtx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(objectKey),
		UploadId: aws.String(s3UploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		s.abortMultipart(context.Background(), objectKey, s3UploadID, uploadID)
		return "", totalParts, fmt.Errorf("complete multipart upload: %w", err)
	}

	return strings.Trim(aws.ToString(completeResult.ETag), "\""), totalParts, nil
}

func (s *Service) uploadPartWithRetry(
	ctx context.Context,
	uploadID string,
	s3UploadID string,
	objectKey string,
	file io.ReaderAt,
	partNumber int32,
	offset int64,
	contentLength int64,
) (string, error) {
	var output *s3.UploadPartOutput
	err := retry.Do(
		func() error {
			if err := ctx.Err(); err != nil {
				return retry.Unrecoverable(err)
			}

			body := io.NewSectionReader(file, offset, contentLength)
			result, err := s.s3.UploadPart(ctx, &s3.UploadPartInput{
				Bucket:     aws.String(s.bucket),
				Key:        aws.String(objectKey),
				UploadId:   aws.String(s3UploadID),
				PartNumber: aws.Int32(partNumber),
				Body:       body,
			})
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
			s.logger.Warn("multipart part retry",
				zap.String("upload_id", uploadID),
				zap.String("s3_upload_id", s3UploadID),
				zap.Int32("part_number", partNumber),
				zap.Uint("attempt", attempt+1),
				zap.Error(err),
			)
		}),
	)
	if err != nil {
		return "", fmt.Errorf("upload part %d failed: %w", partNumber, err)
	}

	return strings.Trim(aws.ToString(output.ETag), "\""), nil
}

func (s *Service) abortMultipart(ctx context.Context, objectKey, s3UploadID, uploadID string) {
	_, err := s.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(objectKey),
		UploadId: aws.String(s3UploadID),
	})
	if err != nil {
		s.logger.Error("failed aborting multipart upload",
			zap.String("upload_id", uploadID),
			zap.String("s3_upload_id", s3UploadID),
			zap.Error(err),
		)
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
