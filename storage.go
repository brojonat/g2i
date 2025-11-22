package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectStorage defines the interface for object storage operations
type ObjectStorage interface {
	Store(ctx context.Context, data []byte, bucket, key, contentType string) (string, error)
	List(ctx context.Context, bucket, prefix string) ([]string, error)
	ListTopLevelFolders(ctx context.Context, bucket string) ([]string, error)
	GetLatestObjectKeyForUser(ctx context.Context, bucket, username string) (string, error)
	Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error
	Delete(ctx context.Context, bucket, prefix string) error
	GetURL(bucket, key string) string
	GetPresignedURL(ctx context.Context, bucket, key string, expires time.Duration) (string, error)
	Stat(ctx context.Context, bucket, key string) (string, error)
}

const (
	S3PlatformR2    = "r2"
	S3PlatformMinio = "minio"
	S3PlatformAWS   = "aws"
	S3PlatformGCS   = "gcs"
)

// S3CompatibleStorage implements ObjectStorage using S3-compatible storage
type S3CompatibleStorage struct {
	Platform       string
	Endpoint       string
	PublicEndpoint string
	Region         string
	AccessKey      string
	SecretKey      string
	UseSSL         bool
}

// NewS3CompatibleStorage creates a new S3-compatible storage instance
func NewS3CompatibleStorage(cfg *Config) *S3CompatibleStorage {
	return &S3CompatibleStorage{
		Platform:       cfg.S3Platform,
		Endpoint:       cfg.S3Endpoint,
		PublicEndpoint: cfg.S3PublicEndpoint,
		Region:         cfg.S3Region,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		UseSSL:         cfg.S3UseSSL,
	}
}

// Store stores content in S3-compatible storage and returns the URL
func (s *S3CompatibleStorage) Store(ctx context.Context, data []byte, bucket, key, contentType string) (string, error) {
	// Create S3-compatible client
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	// Check if bucket exists, create if not
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return "", fmt.Errorf("failed to check bucket existence: %w", err)
	}
	if !exists {
		err = client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{
			Region: s.Region,
		})
		if err != nil {
			return "", fmt.Errorf("failed to create bucket: %w", err)
		}

		// Set bucket policy to allow public read access
		policy := fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Principal": {"AWS": ["*"]},
					"Action": ["s3:GetObject"],
					"Resource": ["arn:aws:s3:::%s/*"]
				}
			]
		}`, bucket)
		err = client.SetBucketPolicy(ctx, bucket, policy)
		if err != nil {
			return "", fmt.Errorf("failed to set bucket policy: %w", err)
		}
	}

	// Upload content to S3-compatible storage
	_, err = client.PutObject(ctx, bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3-compatible storage: %w", err)
	}

	return s.GetURL(bucket, key), nil
}

// Stat checks if an object exists and returns its public URL if it does.
func (s *S3CompatibleStorage) Stat(ctx context.Context, bucket, key string) (string, error) {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	_, err = client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("object %s not found in bucket %s: %w", key, bucket, err)
	}

	return s.GetURL(bucket, key), nil
}

// List lists objects in an S3-compatible bucket with a given prefix.
func (s *S3CompatibleStorage) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	objectCh := client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	var objects []string
	for object := range objectCh {
		if object.Err != nil {
			return nil, fmt.Errorf("failed during object listing: %w", object.Err)
		}
		objects = append(objects, object.Key)
	}
	return objects, nil
}

// ListTopLevelFolders lists "directories" at the root of a bucket.
func (s *S3CompatibleStorage) ListTopLevelFolders(ctx context.Context, bucket string) ([]string, error) {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	objectCh := client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Recursive: false, // We only want top-level folders
	})

	folders := make(map[string]struct{})
	for object := range objectCh {
		if object.Err != nil {
			return nil, fmt.Errorf("failed during object listing: %w", object.Err)
		}
		// In S3, "folders" are just prefixes ending with a "/"
		if strings.HasSuffix(object.Key, "/") {
			folders[strings.TrimSuffix(object.Key, "/")] = struct{}{}
		} else {
			// If it's a file at the top level, its "folder" is the part before the first "/"
			parts := strings.SplitN(object.Key, "/", 2)
			if len(parts) > 0 {
				folders[parts[0]] = struct{}{}
			}
		}
	}

	var folderList []string
	for folder := range folders {
		folderList = append(folderList, folder)
	}

	return folderList, nil
}

// GetLatestObjectKeyForUser finds the most recent object for a given user.
func (s *S3CompatibleStorage) GetLatestObjectKeyForUser(ctx context.Context, bucket, username string) (string, error) {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	prefix := username + "/"
	objectCh := client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	var latestKey string
	var latestTimestamp int64

	for object := range objectCh {
		if object.Err != nil {
			return "", fmt.Errorf("failed during object listing: %w", object.Err)
		}

		// Extract timestamp from key: username/timestamp/content.ext
		parts := strings.Split(strings.TrimPrefix(object.Key, prefix), "/")
		if len(parts) >= 2 {
			timestamp, err := time.Parse(time.RFC3339, object.LastModified.Format(time.RFC3339))
			if err == nil {
				if timestamp.Unix() > latestTimestamp {
					latestTimestamp = timestamp.Unix()
					latestKey = object.Key
				}
			}
		}
	}

	if latestKey == "" {
		return "", fmt.Errorf("no objects found for user: %s", username)
	}

	return latestKey, nil
}

// Copy performs a server-side copy of an object.
func (s *S3CompatibleStorage) Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	srcOpts := minio.CopySrcOptions{
		Bucket: srcBucket,
		Object: srcKey,
	}
	dstOpts := minio.CopyDestOptions{
		Bucket: dstBucket,
		Object: dstKey,
	}

	_, err = client.CopyObject(ctx, dstOpts, srcOpts)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}
	return nil
}

// Delete removes all objects with a given prefix from a bucket.
func (s *S3CompatibleStorage) Delete(ctx context.Context, bucket, prefix string) error {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	// List all objects with the prefix
	objectCh := client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	// Create a channel for objects to delete
	objectsCh := make(chan minio.ObjectInfo)

	go func() {
		defer close(objectsCh)
		for object := range objectCh {
			if object.Err != nil {
				continue
			}
			objectsCh <- object
		}
	}()

	// Remove objects
	errorCh := client.RemoveObjects(ctx, bucket, objectsCh, minio.RemoveObjectsOptions{})
	for err := range errorCh {
		if err.Err != nil {
			return fmt.Errorf("failed to delete object %s: %w", err.ObjectName, err.Err)
		}
	}

	return nil
}

// GetURL returns the PUBLIC URL for a stored object
func (s *S3CompatibleStorage) GetURL(bucket, key string) string {
	protocol := "http"
	if s.UseSSL {
		protocol = "https"
	}
	publicURL := protocol + "://" + s.PublicEndpoint + "/"
	// r2 is different because cloudflare domain routes directly to the bucket
	if s.Platform == S3PlatformR2 {
		publicURL += key
		return publicURL
	}
	publicURL += bucket + "/" + key
	return publicURL
}

// GetPresignedURL generates a presigned URL for accessing an object
func (s *S3CompatibleStorage) GetPresignedURL(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	presignedURL, err := client.PresignedGetObject(ctx, bucket, key, expires, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignedURL.String(), nil
}

// SetupBucketPublicRead sets the bucket policy to allow public read access
func (s *S3CompatibleStorage) SetupBucketPublicRead(ctx context.Context, bucket string) error {
	client, err := minio.New(s.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure: s.UseSSL,
		Region: s.Region,
	})
	if err != nil {
		return fmt.Errorf("failed to create S3-compatible client: %w", err)
	}

	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {"AWS": ["*"]},
				"Action": ["s3:GetObject"],
				"Resource": ["arn:aws:s3:::%s/*"]
			}
		]
	}`, bucket)

	err = client.SetBucketPolicy(ctx, bucket, policy)
	if err != nil {
		return fmt.Errorf("failed to set bucket policy: %w", err)
	}

	return nil
}

// S3Storage implements ObjectStorage using AWS S3
type S3Storage struct {
	Region    string
	AccessKey string
	SecretKey string
}

// NewS3Storage creates a new S3 storage instance
func NewS3Storage(cfg *Config) *S3Storage {
	return &S3Storage{
		Region:    cfg.AWSRegion,
		AccessKey: cfg.AWSAccessKey,
		SecretKey: cfg.AWSSecretKey,
	}
}

// Store stores content in S3 and returns the URL
func (s *S3Storage) Store(ctx context.Context, data []byte, bucket, key, contentType string) (string, error) {
	// In a real implementation, you would use the AWS SDK
	// For now, return a mock S3 URL
	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, s.Region, key)
	return url, nil
}

// List for S3 (mock implementation)
func (s *S3Storage) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	// Mock implementation for AWS S3
	return []string{
		prefix + "user1.png",
		prefix + "user2.png",
	}, nil
}

// ListTopLevelFolders for S3 (mock implementation)
func (s *S3Storage) ListTopLevelFolders(ctx context.Context, bucket string) ([]string, error) {
	// Mock implementation for AWS S3
	return []string{"user1", "user2", "user3"}, nil
}

// GetLatestObjectKeyForUser for S3 (mock implementation)
func (s *S3Storage) GetLatestObjectKeyForUser(ctx context.Context, bucket, username string) (string, error) {
	return fmt.Sprintf("%s/1234567890/content.png", username), nil
}

// Copy for S3 (mock implementation)
func (s *S3Storage) Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	return nil
}

// Delete for S3 (mock implementation)
func (s *S3Storage) Delete(ctx context.Context, bucket, prefix string) error {
	return nil
}

// GetURL returns the URL for a stored object
func (s *S3Storage) GetURL(bucket, key string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, s.Region, key)
}

// GetPresignedURL for S3 (mock implementation)
func (s *S3Storage) GetPresignedURL(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	// Mock implementation - in real AWS S3, you'd use aws-sdk-go v2 to generate presigned URLs
	return s.GetURL(bucket, key), nil
}

// Stat for S3 (mock implementation)
func (s *S3Storage) Stat(ctx context.Context, bucket, key string) (string, error) {
	// Mock implementation for AWS S3
	return s.GetURL(bucket, key), nil
}

// GCSStorage implements ObjectStorage using Google Cloud Storage
type GCSStorage struct {
	ProjectID       string
	CredentialsPath string
}

// NewGCSStorage creates a new GCS storage instance
func NewGCSStorage(cfg *Config) *GCSStorage {
	return &GCSStorage{
		ProjectID:       cfg.GCSProjectID,
		CredentialsPath: cfg.GCSCredentialsPath,
	}
}

// Store stores content in GCS and returns the URL
func (g *GCSStorage) Store(ctx context.Context, data []byte, bucket, key, contentType string) (string, error) {
	// In a real implementation, you would use the GCS client
	// For now, return a mock GCS URL
	url := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, key)
	return url, nil
}

// List for GCS (mock implementation)
func (g *GCSStorage) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	// Mock implementation for GCS
	return []string{
		prefix + "user1.png",
		prefix + "user2.png",
	}, nil
}

// ListTopLevelFolders for GCS (mock implementation)
func (g *GCSStorage) ListTopLevelFolders(ctx context.Context, bucket string) ([]string, error) {
	// Mock implementation for GCS
	return []string{"user1", "user2", "user3"}, nil
}

// GetLatestObjectKeyForUser for GCS (mock implementation)
func (g *GCSStorage) GetLatestObjectKeyForUser(ctx context.Context, bucket, username string) (string, error) {
	return fmt.Sprintf("%s/1234567890/content.png", username), nil
}

// Copy for GCS (mock implementation)
func (g *GCSStorage) Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	return nil
}

// Delete for GCS (mock implementation)
func (g *GCSStorage) Delete(ctx context.Context, bucket, prefix string) error {
	return nil
}

// GetURL returns the URL for a stored object
func (g *GCSStorage) GetURL(bucket, key string) string {
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, key)
}

// GetPresignedURL for GCS (mock implementation)
func (g *GCSStorage) GetPresignedURL(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {
	// Mock implementation - in real GCS, you'd use cloud.google.com/go/storage to generate signed URLs
	return g.GetURL(bucket, key), nil
}

// Stat for GCS (mock implementation)
func (g *GCSStorage) Stat(ctx context.Context, bucket, key string) (string, error) {
	// Mock implementation for GCS
	return g.GetURL(bucket, key), nil
}

// NewObjectStorage creates a new ObjectStorage instance based on the provider
func NewObjectStorage(cfg *Config) ObjectStorage {
	switch strings.ToLower(cfg.StorageProvider) {
	case "aws-s3":
		return NewS3Storage(cfg)
	case "gcs":
		return NewGCSStorage(cfg)
	case "s3":
		fallthrough
	case "minio":
		fallthrough
	default:
		return NewS3CompatibleStorage(cfg)
	}
}

// generateStorageKey generates a unique storage key for content
func generateStorageKey(prefix, contentType string) string {
	timestamp := time.Now().Unix()
	extension := "jpg" // default
	parts := strings.Split(contentType, "/")
	if len(parts) == 2 {
		extension = parts[1]
	}
	return fmt.Sprintf("%s/%d/content.%s", prefix, timestamp, extension)
}
