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

type StackInfo struct {
	Name      string
	StateKey  string
	ConfigKey string
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

func (s *S3Store) ListProjects(ctx context.Context) ([]string, error) {
	iter := s.bucket.List(nil)
	seen := map[string]struct{}{}
	var projects []string
	for {
		obj, err := iter.Next(ctx)
		if err != nil {
			break
		}
		const marker = "/.pulumi/stacks/"
		idx := strings.Index(obj.Key, marker)
		if idx < 0 {
			continue
		}
		suffix := obj.Key[idx+len(marker):]
		if !strings.HasSuffix(suffix, ".json") || strings.HasSuffix(suffix, ".json.bak") {
			continue
		}
		project := obj.Key[:idx]
		if _, ok := seen[project]; !ok {
			seen[project] = struct{}{}
			projects = append(projects, project)
		}
	}
	return projects, nil
}

func (s *S3Store) ListStacks(ctx context.Context, project string) ([]StackInfo, error) {
	prefix := project + "/.pulumi/stacks/"
	iter := s.bucket.List(&blob.ListOptions{Prefix: prefix})

	var stacks []StackInfo
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
		stacks = append(stacks, StackInfo{
			Name:      name,
			StateKey:  key,
			ConfigKey: project + "/Pulumi." + name + ".yaml",
		})
	}
	return stacks, nil
}

func (s *S3Store) ReadState(ctx context.Context, stateKey string) ([]byte, error) {
	data, err := s.bucket.ReadAll(ctx, stateKey)
	if err != nil {
		return nil, fmt.Errorf("reading state %s: %w", stateKey, err)
	}
	return data, nil
}

func (s *S3Store) ReadConfig(ctx context.Context, configKey string) ([]byte, error) {
	data, err := s.bucket.ReadAll(ctx, configKey)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", configKey, err)
	}
	return data, nil
}

func (s *S3Store) HasConfig(ctx context.Context, configKey string) bool {
	exists, _ := s.bucket.Exists(ctx, configKey)
	return exists
}
