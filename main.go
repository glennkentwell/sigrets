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
		fmt.Fprintln(os.Stderr, "usage: sigrets [projectFuzzy stackName.{o|c}.secretName]")
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

func runDirect(ctx context.Context, s3 *store.S3Store, profile, project, arg string) {
	defer s3.Close()

	stackName, source, secretName, err := parseGetArg(arg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	projects, err := s3.ListProjects(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	resolved := fuzzyMatchProject(project, projects)
	if resolved == "" {
		fmt.Fprintf(os.Stderr, "project not found: %q\n", project)
		os.Exit(1)
	}
	if resolved != project {
		fmt.Fprintf(os.Stderr, "using project: %s\n", resolved)
	}

	stacks, err := s3.ListStacks(ctx, resolved)
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

	cloudState, err := state.ExtractCloudState(stackState)
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
		candidates = state.ExtractOutputSecrets(stackState)
	case "config":
		histKey := s3.LatestHistoryKey(ctx, resolved, stackName)
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

// fuzzyMatchProject returns the best matching project from the list given a query.
// Exact match wins; otherwise falls back to substring match on the last path segment
// then full path. Returns "" if nothing matches.
func fuzzyMatchProject(query string, projects []string) string {
	query = strings.ToLower(query)
	for _, p := range projects {
		if strings.ToLower(p) == query {
			return p
		}
	}
	for _, p := range projects {
		seg := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			seg = p[i+1:]
		}
		if strings.Contains(strings.ToLower(seg), query) {
			return p
		}
	}
	// substring match on full path
	for _, p := range projects {
		if strings.Contains(strings.ToLower(p), query) {
			return p
		}
	}
	return ""
}
