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
		fmt.Fprintln(os.Stderr, "usage: sigrets get [stackName.{output|out|o|config|cfg|c}.secretName]")
		os.Exit(1)
	}

	if flag.Arg(0) != "get" {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", flag.Arg(0))
		os.Exit(1)
	}

	ctx := context.Background()

	s3, err := store.NewS3Store(ctx, cfg.bucket, cfg.project, cfg.region, cfg.profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer s3.Close()

	if flag.Arg(1) == "" {
		runTUI(ctx, s3)
		return
	}

	runDirect(ctx, s3, flag.Arg(1))
}

func parseFlags() config {
	var cfg config

	flag.StringVar(&cfg.bucket, "bucket", os.Getenv("SIGRETS_BUCKET"), "S3 bucket name")
	flag.StringVar(&cfg.bucket, "b", os.Getenv("SIGRETS_BUCKET"), "S3 bucket name (shorthand)")
	flag.StringVar(&cfg.project, "project", os.Getenv("SIGRETS_PROJECT"), "Pulumi project path prefix in bucket")
	flag.StringVar(&cfg.project, "p", os.Getenv("SIGRETS_PROJECT"), "Pulumi project path prefix (shorthand)")
	flag.StringVar(&cfg.region, "region", envOrDefault("SIGRETS_REGION", envOrDefault("AWS_DEFAULT_REGION", "ap-southeast-2")), "AWS region")
	flag.StringVar(&cfg.region, "r", envOrDefault("SIGRETS_REGION", envOrDefault("AWS_DEFAULT_REGION", "ap-southeast-2")), "AWS region (shorthand)")
	flag.StringVar(&cfg.profile, "profile", os.Getenv("AWS_PROFILE"), "AWS named profile")
	flag.Parse()

	if cfg.bucket == "" {
		fmt.Fprintln(os.Stderr, "error: --bucket is required (or set SIGRETS_BUCKET)")
		os.Exit(1)
	}
	if cfg.project == "" {
		fmt.Fprintln(os.Stderr, "error: --project is required (or set SIGRETS_PROJECT)")
		os.Exit(1)
	}

	return cfg
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runTUI(ctx context.Context, s3 *store.S3Store) {
	stacks, err := s3.ListStacks(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(stacks) == 0 {
		fmt.Fprintln(os.Stderr, "no stacks found")
		os.Exit(1)
	}

	m := tui.New(ctx, s3, stacks)
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

func runDirect(ctx context.Context, s3 *store.S3Store, arg string) {
	stackName, source, secretName, err := parseGetArg(arg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	stacks, err := s3.ListStacks(ctx)
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

	switch source {
	case "output":
		secrets := state.ExtractOutputSecrets(stackState)
		for _, s := range secrets {
			if s.Name == secretName {
				printSecret(decr, s)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "secret not found: %s\n", arg)
		os.Exit(1)

	case "config":
		if !s3.HasConfig(ctx, target.ConfigKey) {
			fmt.Fprintln(os.Stderr, "no config file found for stack")
			os.Exit(1)
		}
		cfgData, err := s3.ReadConfig(ctx, target.ConfigKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cfg, err := state.ParseStackConfig(cfgData)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		secrets := state.ExtractConfigSecrets(cfg)
		for _, s := range secrets {
			if s.Name == secretName || strings.HasSuffix(s.Name, ":"+secretName) {
				printSecret(decr, s)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "secret not found: %s\n", arg)
		os.Exit(1)
	}
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
		return "", "", "", fmt.Errorf("invalid path %q: expected stackName.{output|out|o|config|cfg|c}.secretName", arg)
	}

	stackName = parts[0]
	secretName = parts[2]

	switch strings.ToLower(parts[1]) {
	case "output", "out", "o":
		source = "output"
	case "config", "cfg", "c":
		source = "config"
	default:
		return "", "", "", fmt.Errorf("invalid source %q: use output/out/o or config/cfg/c", parts[1])
	}
	return
}

func extractCloudState(stackState *state.StackState) (state.CloudSecretsState, error) {
	sp := stackState.Deployment.SecretsProviders
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
