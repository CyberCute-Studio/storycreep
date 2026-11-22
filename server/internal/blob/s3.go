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

// partSize is how much of a blob that a Writer writes it holds in memory
// before it sends it as a part of a multipart upload, and maxParts how many
// parts S3 takes of one, so that a blob may have up to 160 GiB.
const (
	partSize = 16 << 20
	maxParts = 10000
)

// abortTimeout is how long a Writer waits for the storage to discard the
// parts of an upload that failed, whatever became of its context.
const abortTimeout = 30 * time.Second

// Create returns a Writer that sends the blob as one object if it fits in a
// part, and as a multipart upload otherwise, which S3 makes visible whole once
// it is completed.
func (s *S3) Create(ctx context.Context, key, contentType string) (Writer, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &s3Writer{s: s, ctx: ctx, key: key, contentType: contentType, buf: make([]byte, 0, partSize)}, nil
}

// s3Writer is the S3 driver's Writer. buf holds what no part has sent yet, and
// upload names the multipart upload once the first part has started it.
type s3Writer struct {
	s                *S3
	ctx              context.Context
	key, contentType string
	buf              []byte
	upload           *string
	parts            []types.CompletedPart
	// ended says that Commit or Abort has been called.
	ended bool
}

// Write adds p to the part that is being filled, and sends each part that
// fills up.
func (w *s3Writer) Write(p []byte) (int, error) {
	if w.ended {
		return 0, fmt.Errorf("blob: write %s: the writer has ended", w.key)
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), partSize-len(w.buf))
		w.buf = append(w.buf, p[:n]...)
		p = p[n:]
		written += n
		if len(w.buf) == partSize {
			if err := w.flush(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// flush sends what buf holds as the next part, starting the upload if this
// is its first.
func (w *s3Writer) flush() error {
	if len(w.parts) == maxParts {
		return fmt.Errorf("blob: store %s in the bucket: it needs more than %d parts", w.key, maxParts)
	}
	if w.upload == nil {
		out, err := w.s.client.CreateMultipartUpload(w.ctx, &s3.CreateMultipartUploadInput{
			Bucket:      aws.String(w.s.bucket),
			Key:         aws.String(w.key),
			ContentType: aws.String(w.contentType),
		})
		if err != nil {
			return fmt.Errorf("blob: start the upload of %s to the bucket: %w", w.key, err)
		}
		w.upload = out.UploadId
	}
	number := aws.Int32(int32(len(w.parts) + 1))
	out, err := w.s.client.UploadPart(w.ctx, &s3.UploadPartInput{
		Bucket:        aws.String(w.s.bucket),
		Key:           aws.String(w.key),
		UploadId:      w.upload,
		PartNumber:    number,
		Body:          bytes.NewReader(w.buf),
		ContentLength: aws.Int64(int64(len(w.buf))),
	})
	if err != nil {
		return fmt.Errorf("blob: send part %d of %s to the bucket: %w", *number, w.key, err)
	}
	w.parts = append(w.parts, types.CompletedPart{ETag: out.ETag, PartNumber: number})
	w.buf = w.buf[:0]
	return nil
}

// Commit stores what a blob of less than a part holds as one object, and
// otherwise sends the last part and completes the upload. A failure discards
// the upload's parts.
func (w *s3Writer) Commit() (err error) {
	if w.ended {
		return fmt.Errorf("blob: store %s: the writer has ended", w.key)
	}
	if w.upload == nil {
		w.ended = true
		return w.s.Put(w.ctx, w.key, w.buf, w.contentType)
	}
	defer func() {
		if err != nil {
			_ = w.Abort()
		}
		w.ended = true
	}()
	if len(w.buf) > 0 {
		if err := w.flush(); err != nil {
			return err
		}
	}
	_, err = w.s.client.CompleteMultipartUpload(w.ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(w.s.bucket),
		Key:             aws.String(w.key),
		UploadId:        w.upload,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: w.parts},
	})
	if err != nil {
		return fmt.Errorf("blob: complete the upload of %s to the bucket: %w", w.key, err)
	}
	return nil
}

// Abort discards the upload's parts, if it has started, however the Writer's
// context ended.
func (w *s3Writer) Abort() error {
	if w.ended {
		return nil
	}
	w.ended = true
	w.buf = nil
	if w.upload == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(w.ctx), abortTimeout)
	defer cancel()
	_, err := w.s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(w.s.bucket),
		Key:      aws.String(w.key),
		UploadId: w.upload,
	})
	if err != nil {
		return fmt.Errorf("blob: discard the upload of %s to the bucket: %w", w.key, err)
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
