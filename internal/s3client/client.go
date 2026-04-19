package s3client

import (
	"context"
	"fmt"
	"strings"

	"big-file-service/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	S3      *s3.Client
	Presign *s3.PresignClient
	Bucket  string
}

func New(ctx context.Context, awsCfg config.AWSConfig, s3Cfg config.S3Config) (*Client, error) {
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(awsCfg.Region),
	}

	if strings.TrimSpace(awsCfg.AccessKeyID) != "" && strings.TrimSpace(awsCfg.SecretAccessKey) != "" {
		options = append(options,
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				awsCfg.AccessKeyID,
				awsCfg.SecretAccessKey,
				awsCfg.SessionToken,
			)),
		)
	}

	loadedCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	s3Client := s3.NewFromConfig(loadedCfg, func(options *s3.Options) {
		options.UsePathStyle = s3Cfg.ForcePathStyle
		if strings.TrimSpace(s3Cfg.EndpointURL) != "" {
			options.BaseEndpoint = aws.String(strings.TrimSpace(s3Cfg.EndpointURL))
		}
	})

	return &Client{
		S3:      s3Client,
		Presign: s3.NewPresignClient(s3Client),
		Bucket:  s3Cfg.Bucket,
	}, nil
}
