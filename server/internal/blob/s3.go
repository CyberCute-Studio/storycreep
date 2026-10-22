package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

// S3 is the S3 driver: it keeps each blob as an object of a bucket of an
// S3-compatible object storage, under its key, and gives out URLs that it
// signs for reading one (ADR-009). It addresses the bucket in the path of its
// URLs, as https://host/bucket/key, which every S3-compatible storage takes,
// and reads its settings from the configuration alone: no AWS environment
// variable or file.
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

var _ Store = (*S3)(nil)

// S3Options configure OpenS3.
type S3Options struct {
	// HTTPClient sends the driver's requests, or nil for the SDK's own.
	// Tests set it.
	HTTPClient *http.Client
}

// OpenS3 returns an S3 driver of the bucket that cfg names. It sends no
// request: a bucket that does not exist, or keys that cannot use it, fail
// the first operation.
func OpenS3(cfg config.S3, opts S3Options) *S3 {
	awsCfg := aws.Config{
		Region: cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey.Reveal(),
			""),
		// Not every S3-compatible storage takes the checksums that the SDK
		// adds by default, so it adds them only where S3 requires one.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if opts.HTTPClient != nil {
		awsCfg.HTTPClient = opts.HTTPClient
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint())
		o.UsePathStyle = true
	})
	return &S3{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}
}

// Put stores the object, which S3 makes visible whole once it has all of it.
func (s *S3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("blob: store %s in the bucket: %w", key, err)
	}
	return nil
}

// Open gets the object.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	if err := CheckKey(key); err != nil {
		return nil, Info{}, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, Info{}, s3Error(key, err)
	}
	return out.Body, Info{Key: key, Size: aws.ToInt64(out.ContentLength), Modified: aws.ToTime(out.LastModified)},
		nil
}

// Stat heads the object.
func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	if err := CheckKey(key); err != nil {
		return Info{}, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return Info{}, s3Error(key, err)
	}
	return Info{Key: key, Size: aws.ToInt64(out.ContentLength), Modified: aws.ToTime(out.LastModified)}, nil
}

// Delete deletes the object, which S3 answers alike whether it exists.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil && !errors.Is(s3Error(key, err), ErrNotFound) {
		return fmt.Errorf("blob: delete %s from the bucket: %w", key, err)
	}
	return nil
}

// List lists the objects of prefix, a page at a time, which S3 returns in
// the order of their keys, from the first after after (StartAfter).
func (s *S3) List(ctx context.Context, prefix, after string, fn func(Info) error) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	in := &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)}
	if after != "" {
		in.StartAfter = aws.String(after)
	}
	pages := s3.NewListObjectsV2Paginator(s.client, in)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("blob: list %s in the bucket: %w", prefix, err)
		}
		for _, o := range page.Contents {
			key := aws.ToString(o.Key)
			// An object that another program put in the bucket under a key
			// that this package would not write is not a blob of it.
			if CheckKey(key) != nil {
				continue
			}
			if err := fn(Info{Key: key, Size: aws.ToInt64(o.Size), Modified: aws.ToTime(o.LastModified)}); err != nil {
				return err
			}
		}
	}
	return nil
}

// SignedURL presigns a GET of the object, which overrides the content type,
// the Content-Disposition, and the Cache-Control of the answer, so that the
// signature fixes all three: private, for the browser alone, and a max-age
// of ttl.
func (s *S3) SignedURL(ctx context.Context, key string, ttl time.Duration, contentType, disposition string) (string,
	bool, error) {
	if err := CheckKey(key); err != nil {
		return "", false, err
	}
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentType:        aws.String(contentType),
		ResponseContentDisposition: aws.String(disposition),
		ResponseCacheControl:       aws.String(CacheControl(ttl)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", false, fmt.Errorf("blob: sign a URL of %s: %w", key, err)
	}
	return req.URL, true, nil
}

// s3Error returns ErrNotFound for an error that says that the object does
// not exist, and the error, with the key, otherwise. Amazon S3 answers a
// request of an object that does not exist with 403 Forbidden, rather than
// 404, when the keys may not list the bucket, which they must, so the error
// of a 403 says so, as the operator who reads it in the log needs to know.
func s3Error(key string, err error) error {
	var noKey *types.NoSuchKey
	var notFound *types.NotFound
	var api smithy.APIError
	var res interface{ HTTPStatusCode() int }
	switch {
	case errors.As(err, &noKey), errors.As(err, &notFound):
		return ErrNotFound
	case errors.As(err, &api) && (api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound"):
		return ErrNotFound
	case errors.As(err, &res) && res.HTTPStatusCode() == http.StatusForbidden:
		return fmt.Errorf("blob: read %s from the bucket, which refused it: S3 refuses a request of an object that "+
			"does not exist, rather than answer that it does not, when the keys may not list the bucket, which they "+
			"must: %w", key, err)
	}
	return fmt.Errorf("blob: read %s from the bucket: %w", key, err)
}
