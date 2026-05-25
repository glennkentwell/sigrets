package store

import (
	"context"
	"fmt"
	"strings"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/s3blob"
)

type S3Store struct {
	bucket *blob.Bucket
}

type ProjectInfo struct {
	Name     string
	StateKey string
}

func NewS3Store(ctx context.Context, bucketName, region, profile string) (*S3Store, error) {
	url := fmt.Sprintf("s3://%s?region=%s&awssdk=v2", bucketName, region)
	if profile != "" {
		url += "&profile=" + profile
	}
	b, err := blob.OpenBucket(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("opening s3 bucket %s: %w", bucketName, err)
	}
	return &S3Store{bucket: b}, nil
}

func (s *S3Store) Close() error {
	return s.bucket.Close()
}

func (s *S3Store) ListBackends(ctx context.Context) ([]string, error) {
	const pulumiDir = "/.pulumi/"
	iter := s.bucket.List(&blob.ListOptions{Delimiter: pulumiDir})
	var backends []string
	for {
		obj, err := iter.Next(ctx)
		if err != nil {
			break
		}
		if !obj.IsDir {
			continue
		}
		backend := strings.TrimSuffix(obj.Key, pulumiDir)
		if backend != "" {
			backends = append(backends, backend)
		}
	}
	return backends, nil
}

func (s *S3Store) ListProjects(ctx context.Context, backend string) ([]ProjectInfo, error) {
	prefix := backend + "/.pulumi/stacks/"
	iter := s.bucket.List(&blob.ListOptions{Prefix: prefix, Delimiter: "/"})

	var projects []ProjectInfo
	for {
		obj, err := iter.Next(ctx)
		if err != nil {
			break
		}
		key := obj.Key
		if !strings.HasSuffix(key, ".json") || strings.HasSuffix(key, ".json.bak") {
			continue
		}
		name := strings.TrimPrefix(key, prefix)
		name = strings.TrimSuffix(name, ".json")
		projects = append(projects, ProjectInfo{
			Name:     name,
			StateKey: key,
		})
	}
	return projects, nil
}

func (s *S3Store) ReadState(ctx context.Context, stateKey string) ([]byte, error) {
	data, err := s.bucket.ReadAll(ctx, stateKey)
	if err != nil {
		return nil, fmt.Errorf("reading state %s: %w", stateKey, err)
	}
	return data, nil
}

func (s *S3Store) LatestHistoryKey(ctx context.Context, backend, project string) string {
	prefix := backend + "/.pulumi/history/" + project + "/"
	iter := s.bucket.List(&blob.ListOptions{Prefix: prefix})
	latest := ""
	for {
		obj, err := iter.Next(ctx)
		if err != nil {
			break
		}
		if strings.HasSuffix(obj.Key, ".history.json") {
			latest = obj.Key
		}
	}
	return latest
}

func (s *S3Store) ReadBytes(ctx context.Context, key string) ([]byte, error) {
	data, err := s.bucket.ReadAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	return data, nil
}
