package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectStorage defines the interface for object storage operations
type ObjectStorage interface {
	Store(ctx context.Context, data []byte, bucket, key, contentType string) (string, error)
	ListTopLevelFolders(ctx context.Context, bucket string) ([]string, error)
	GetURL(bucket, key string) string
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
func NewS3CompatibleStorage() *S3CompatibleStorage {
	return &S3CompatibleStorage{
		Platform:       os.Getenv("S3_PLATFORM"),
		Endpoint:       os.Getenv("S3_ENDPOINT"),
		PublicEndpoint: os.Getenv("S3_PUBLIC_ENDPOINT"),
		Region:         os.Getenv("S3_REGION"),
		AccessKey:      os.Getenv("S3_ACCESS_KEY"),
		SecretKey:      os.Getenv("S3_SECRET_KEY"),
		UseSSL:         os.Getenv("S3_USE_SSL") == "true",
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

// S3Storage implements ObjectStorage using AWS S3
type S3Storage struct {
	Region    string
	AccessKey string
	SecretKey string
}

// NewS3Storage creates a new S3 storage instance
func NewS3Storage() *S3Storage {
	return &S3Storage{
		Region:    os.Getenv("AWS_REGION"),
		AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	}
}

// Store stores content in S3 and returns the URL
func (s *S3Storage) Store(ctx context.Context, data []byte, bucket, key, contentType string) (string, error) {
	// In a real implementation, you would use the AWS SDK
	// For now, return a mock S3 URL
	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, s.Region, key)
	return url, nil
}

// ListTopLevelFolders for S3 (mock implementation)
func (s *S3Storage) ListTopLevelFolders(ctx context.Context, bucket string) ([]string, error) {
	// Mock implementation for AWS S3
	return []string{"user1", "user2", "user3"}, nil
}

// GetURL returns the URL for a stored object
func (s *S3Storage) GetURL(bucket, key string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, s.Region, key)
}

// GCSStorage implements ObjectStorage using Google Cloud Storage
type GCSStorage struct {
	ProjectID       string
	CredentialsPath string
}

// NewGCSStorage creates a new GCS storage instance
func NewGCSStorage() *GCSStorage {
	return &GCSStorage{
		ProjectID:       os.Getenv("GCS_PROJECT_ID"),
		CredentialsPath: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
	}
}

// Store stores content in GCS and returns the URL
func (g *GCSStorage) Store(ctx context.Context, data []byte, bucket, key, contentType string) (string, error) {
	// In a real implementation, you would use the GCS client
	// For now, return a mock GCS URL
	url := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, key)
	return url, nil
}

// ListTopLevelFolders for GCS (mock implementation)
func (g *GCSStorage) ListTopLevelFolders(ctx context.Context, bucket string) ([]string, error) {
	// Mock implementation for GCS
	return []string{"user1", "user2", "user3"}, nil
}

// GetURL returns the URL for a stored object
func (g *GCSStorage) GetURL(bucket, key string) string {
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, key)
}

// NewObjectStorage creates a new ObjectStorage instance based on the provider
func NewObjectStorage(provider string) ObjectStorage {
	switch strings.ToLower(provider) {
	case "aws-s3":
		return NewS3Storage()
	case "gcs":
		return NewGCSStorage()
	case "s3":
		fallthrough
	case "minio":
		fallthrough
	default:
		return NewS3CompatibleStorage()
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
