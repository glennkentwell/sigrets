package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"glenn.io/sigrets/internal/crypto"
	"glenn.io/sigrets/internal/state"
	"glenn.io/sigrets/internal/store"
	"glenn.io/sigrets/internal/tui"
)

type config struct {
	bucket  string
	project string
	region  string
	profile string
}

func main() {
	cfg := parseFlags()

	if len(flag.Args()) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sigrets get [stackName.{o|c}.secretName]")
		os.Exit(1)
	}

	if flag.Arg(0) != "get" {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", flag.Arg(0))
		os.Exit(1)
	}

	ctx := context.Background()

	s3, err := store.NewS3Store(ctx, cfg.bucket, cfg.region, cfg.profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer s3.Close()

	if flag.Arg(1) == "" {
		runTUI(ctx, s3)
		return
	}

	if cfg.project == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required for direct get (or omit the path argument to use the TUI)")
		os.Exit(1)
	}
	runDirect(ctx, s3, cfg.project, flag.Arg(1))
}

func parseFlags() config {
	var cfg config

	const defaultBucket = "my-pulumi-state"
	flag.StringVar(&cfg.bucket, "bucket", envOrDefault("SIGRETS_BUCKET", defaultBucket), "S3 bucket name")
	flag.StringVar(&cfg.bucket, "b", envOrDefault("SIGRETS_BUCKET", defaultBucket), "S3 bucket name (shorthand)")
	flag.StringVar(&cfg.project, "project", os.Getenv("SIGRETS_PROJECT"), "Pulumi project path prefix (optional for TUI)")
	flag.StringVar(&cfg.project, "p", os.Getenv("SIGRETS_PROJECT"), "Pulumi project path prefix (shorthand)")
	flag.StringVar(&cfg.region, "region", envOrDefault("SIGRETS_REGION", envOrDefault("AWS_DEFAULT_REGION", "ap-southeast-2")), "AWS region")
	flag.StringVar(&cfg.region, "r", envOrDefault("SIGRETS_REGION", envOrDefault("AWS_DEFAULT_REGION", "ap-southeast-2")), "AWS region (shorthand)")
	flag.StringVar(&cfg.profile, "profile", os.Getenv("AWS_PROFILE"), "AWS named profile")
	flag.Parse()

	return cfg
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runTUI(ctx context.Context, s3 *store.S3Store) {
	m := tui.New(ctx, s3)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	model := final.(tui.Model)
	if model.Err() != nil {
		fmt.Fprintln(os.Stderr, model.Err())
		os.Exit(1)
	}
	if v := model.Result(); v != "" {
		fmt.Print(v)
	}
}

func runDirect(ctx context.Context, s3 *store.S3Store, project, arg string) {
	stackName, source, secretName, err := parseGetArg(arg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	stacks, err := s3.ListStacks(ctx, project)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var target *store.StackInfo
	for _, s := range stacks {
		if s.Name == stackName {
			s := s
			target = &s
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "stack not found: %q\n", stackName)
		os.Exit(1)
	}

	stateData, err := s3.ReadState(ctx, target.StateKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	stackState, err := state.ParseStackState(stateData)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cloudState, err := extractCloudState(stackState)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	decr, err := crypto.NewDecryptor(ctx, cloudState.URL, cloudState.EncryptedKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var candidates []state.Secret

	switch source {
	case "output":
		candidates = state.ExtractOutputSecrets(stackState)
	case "config":
		histKey := s3.LatestHistoryKey(ctx, project, stackName)
		if histKey == "" {
			fmt.Fprintln(os.Stderr, "no history file found for stack")
			os.Exit(1)
		}
		histData, err := s3.ReadBytes(ctx, histKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		hist, err := state.ParseHistoryRecord(histData)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		candidates = state.ExtractHistorySecrets(hist)
	}

	for _, s := range candidates {
		if s.Name == secretName {
			printSecret(decr, s)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "secret not found: %s\n", arg)
	os.Exit(1)
}

func printSecret(decr *crypto.Decryptor, s state.Secret) {
	if s.Ciphertext == "" {
		fmt.Print(s.Value)
		return
	}
	plaintext, err := decr.Decrypt(s.Ciphertext)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(plaintext)
}

func parseGetArg(arg string) (stackName, source, secretName string, err error) {
	parts := strings.SplitN(arg, ".", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid path %q: expected stackName.{o|c}.secretName", arg)
	}
	stackName = parts[0]
	secretName = parts[2]
	switch strings.ToLower(parts[1]) {
	case "o", "out", "output":
		source = "output"
	case "c", "cfg", "config":
		source = "config"
	default:
		return "", "", "", fmt.Errorf("invalid source %q: use o/out/output or c/cfg/config", parts[1])
	}
	return
}

func extractCloudState(stackState *state.StackState) (state.CloudSecretsState, error) {
	if stackState.Checkpoint.Latest == nil {
		return state.CloudSecretsState{}, fmt.Errorf("stack has no deployment (empty checkpoint)")
	}
	sp := stackState.Checkpoint.Latest.SecretsProviders
	if sp == nil {
		return state.CloudSecretsState{}, fmt.Errorf("stack has no secrets provider")
	}
	if sp.Type != "cloud" {
		return state.CloudSecretsState{}, fmt.Errorf("unsupported secrets provider %q (only \"cloud\"/KMS supported)", sp.Type)
	}
	var cs state.CloudSecretsState
	if err := json.Unmarshal(sp.State, &cs); err != nil {
		return state.CloudSecretsState{}, fmt.Errorf("parsing cloud secrets state: %w", err)
	}
	return cs, nil
}
