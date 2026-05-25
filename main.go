package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"glenn.io/sigrets/internal/cfg"
	"glenn.io/sigrets/internal/crypto"
	"glenn.io/sigrets/internal/state"
	"glenn.io/sigrets/internal/store"
	"glenn.io/sigrets/internal/tui"
)

type config struct {
	bucket       string
	bucketSource string
	region       string
	profile      string
}

func main() {
	c := parseFlags()

	ctx := context.Background()

	switch len(flag.Args()) {
	case 0:
		// no args → TUI
		s3 := mustOpenStore(ctx, c)
		runTUI(ctx, s3, c)

	case 2:
		s3 := mustOpenStore(ctx, c)
		defer s3.Close()
		runDirect(ctx, s3, c.profile, flag.Arg(0), flag.Arg(1))

	default:
		fmt.Fprintln(os.Stderr, "usage: sigrets [backendFuzzy projectName.{o|c}.secretName]")
		os.Exit(1)
	}
}

func parseFlags() config {
	var c config

	flag.StringVar(&c.bucket, "bucket", "", "S3 bucket name")
	flag.StringVar(&c.bucket, "b", "", "S3 bucket name (shorthand)")
	flag.StringVar(&c.region, "region", envOrDefault("SIGRETS_REGION", envOrDefault("AWS_DEFAULT_REGION", "ap-southeast-2")), "AWS region")
	flag.StringVar(&c.region, "r", envOrDefault("SIGRETS_REGION", envOrDefault("AWS_DEFAULT_REGION", "ap-southeast-2")), "AWS region (shorthand)")
	flag.StringVar(&c.profile, "profile", os.Getenv("AWS_PROFILE"), "AWS named profile")
	flag.Parse()

	c.bucket, c.bucketSource = resolveBucket(c.bucket)
	c.profile = resolveProfile(c.profile)
	return c
}

func resolveBucket(flagVal string) (bucket, source string) {
	if flagVal != "" {
		return flagVal, "flag"
	}
	if v := os.Getenv("SIGRETS_BUCKET"); v != "" {
		return v, "SIGRETS_BUCKET env"
	}
	if f, err := cfg.Load(); err == nil && f.Bucket != "" {
		return f.Bucket, cfg.Path()
	}
	fmt.Fprint(os.Stderr, "S3 bucket name: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	v := strings.TrimSpace(scanner.Text())
	if v == "" {
		fmt.Fprintln(os.Stderr, "bucket name required")
		os.Exit(1)
	}
	saveConfig(func(f *cfg.File) { f.Bucket = v })
	return v, "prompt"
}

func resolveProfile(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("AWS_PROFILE"); v != "" {
		return v
	}
	if f, err := cfg.Load(); err == nil && f.Profile != "" {
		return f.Profile
	}
	fmt.Fprint(os.Stderr, "AWS profile name: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	v := strings.TrimSpace(scanner.Text())
	if v == "" {
		fmt.Fprintln(os.Stderr, "AWS profile name required")
		os.Exit(1)
	}
	saveConfig(func(f *cfg.File) { f.Profile = v })
	return v
}

func saveConfig(update func(*cfg.File)) {
	f, err := cfg.Load()
	if err != nil {
		f = &cfg.File{}
	}
	update(f)
	if err := cfg.Save(f); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save config: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "saved to %s\n", cfg.Path())
	}
}

func mustOpenStore(ctx context.Context, c config) *store.S3Store {
	s3, err := store.NewS3Store(ctx, c.bucket, c.region, c.profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return s3
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runTUI(ctx context.Context, s3 *store.S3Store, c config) {
	m := tui.New(ctx, s3, c.bucketSource, c.profile)
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

func runDirect(ctx context.Context, s3 *store.S3Store, profile, backend, arg string) {
	defer s3.Close()

	projectName, source, secretName, err := parseGetArg(arg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	backends, err := s3.ListBackends(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	resolved := fuzzyMatchBackend(backend, backends)
	if resolved == "" {
		fmt.Fprintf(os.Stderr, "backend not found: %q\n", backend)
		os.Exit(1)
	}
	if resolved != backend {
		fmt.Fprintf(os.Stderr, "using backend: %s\n", resolved)
	}

	projects, err := s3.ListProjects(ctx, resolved)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var target *store.ProjectInfo
	for _, p := range projects {
		if p.Name == projectName {
			p := p
			target = &p
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "project not found: %q\n", projectName)
		os.Exit(1)
	}

	stateData, err := s3.ReadState(ctx, target.StateKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	projectState, err := state.ParseProjectState(stateData)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cloudState, err := state.ExtractCloudState(projectState)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	decr, err := crypto.NewDecryptor(ctx, cloudState.URL, cloudState.EncryptedKey, profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var candidates []state.Secret
	switch source {
	case "output":
		candidates = state.ExtractOutputSecrets(projectState)
	case "config":
		histKey := s3.LatestHistoryKey(ctx, resolved, projectName)
		if histKey == "" {
			fmt.Fprintln(os.Stderr, "no history file found for project")
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

func parseGetArg(arg string) (projectName, source, secretName string, err error) {
	parts := strings.SplitN(arg, ".", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid path %q: expected projectName.{o|c}.secretName", arg)
	}
	projectName = parts[0]
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

// fuzzyMatchBackend returns the best matching backend from the list given a query.
// Exact match wins; otherwise falls back to substring match on the last path segment
// then full path. Returns "" if nothing matches.
func fuzzyMatchBackend(query string, backends []string) string {
	query = strings.ToLower(query)
	for _, b := range backends {
		if strings.ToLower(b) == query {
			return b
		}
	}
	for _, b := range backends {
		seg := b
		if i := strings.LastIndex(b, "/"); i >= 0 {
			seg = b[i+1:]
		}
		if strings.Contains(strings.ToLower(seg), query) {
			return b
		}
	}
	// substring match on full path
	for _, b := range backends {
		if strings.Contains(strings.ToLower(b), query) {
			return b
		}
	}
	return ""
}
