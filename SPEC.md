# sigrets — Spec & Todo

> Pulumi secret reader for S3-backed stacks. No Pulumi CLI required.

---

## What it does

`sigrets` reads Pulumi stack state files directly from S3, decrypts secrets using AWS KMS, and presents them as plaintext — either via a single CLI command or an interactive TUI.

---

## Decryption chain

Pulumi uses **envelope encryption** for secrets in state files:

```
secrets_providers.state.encryptedkey   (base64-encoded, KMS-encrypted 32-byte data key)
        │
        ▼ KMS Decrypt (awskms:// via gocloud.dev)
        │
plaintext 32-byte data key
        │
        ▼ AES-256-GCM (symmetricCrypter)
        │
secret.ciphertext  →  "v1:<base64-nonce>:<base64-ciphertext>"
        │
        ▼
plaintext secret value (JSON-encoded)
```

**Secret marker in state JSON:**
```json
{
  "4dabf18193072939515e22adb298388d": "1b47061264138c4ac30d75fd1eb44270",
  "ciphertext": "v1:eWe6YqrqVdfja2yR:lNdAi1HLHgcvlMta4bRcoqQgrROnMQJOLA=="
}
```

**Config secrets** (in `Pulumi.<stack>.yaml`) use the same `v1:` format under a `secure:` key.

---

## S3 layout

```
s3://<bucket>/
  <project>/.pulumi/stacks/<stack>.json     ← state file
  <project>/.pulumi/stacks/<stack>.json.bak ← previous checkpoint (ignore)
```

State files live under project-scoped paths. The bucket name and project are configured via flags or a config file.

---

## CLI interface

### Direct get

```bash
# Outputs
sigrets get stackName.output.secretName
sigrets get stackName.out.secretName
sigrets get stackName.o.secretName

# Config secrets
sigrets get stackName.config.secretName
sigrets get stackName.cfg.secretName
sigrets get stackName.c.secretName
```

Prints the raw plaintext value to stdout (no trailing newline — pipe-friendly).

On error, exits non-zero and prints to stderr.

### Interactive TUI

```bash
sigrets get
```

Launches a TUI:
1. **Stack list** — lists all stacks found in S3 (filtered to `.json`, excluding `.bak`)
2. **Secret list** — after selecting a stack, shows all secrets (outputs + config combined, labelled by type)
3. **Copy / print** — selecting a secret prints it to stdout and exits

### Global flags

| Flag | Default | Description |
|---|---|---|
| `--bucket` / `-b` | (required) | S3 bucket name |
| `--project` / `-p` | (required) | Pulumi project path prefix in the bucket |
| `--region` / `-r` | `AWS_DEFAULT_REGION` or `ap-southeast-2` | AWS region |
| `--profile` | `AWS_PROFILE` env var | AWS named profile |

Flags can also be set via environment variables:

| Env var | Flag |
|---|---|
| `SIGRETS_BUCKET` | `--bucket` |
| `SIGRETS_PROJECT` | `--project` |
| `SIGRETS_REGION` | `--region` |
| `AWS_PROFILE` | `--profile` |

---

## Package structure

```
sigrets/
├── main.go                        ← entry point, flag parsing, command dispatch
├── internal/
│   ├── state/
│   │   ├── types.go               ← Pulumi state JSON structs
│   │   ├── config.go              ← Pulumi.<stack>.yaml struct + parsing
│   │   └── secrets.go             ← secret detection, extraction helpers
│   ├── store/
│   │   └── s3.go                  ← S3 reader: list stacks, read state, read config
│   ├── crypto/
│   │   └── decrypt.go             ← KMS data key decrypt + AES-256-GCM symmetric decrypt
│   └── tui/
│       ├── model.go               ← bubbletea model (stack list → secret list)
│       └── styles.go              ← lipgloss styles
└── SPEC.md
```

---

## Key types

### State file (`<stack>.json`)

```go
type StackState struct {
    Version    int        `json:"version"`
    Deployment Deployment `json:"deployment"`
}

type Deployment struct {
    SecretsProviders *SecretsProvider `json:"secrets_providers"`
    Resources        []Resource       `json:"resources"`
}

type SecretsProvider struct {
    Type  string          `json:"type"`  // "cloud"
    State json.RawMessage `json:"state"` // cloudSecretsManagerState
}

type CloudSecretsState struct {
    URL          string `json:"url"`          // e.g. "awskms://alias/my-key?region=ap-southeast-2&awssdk=v2"
    EncryptedKey []byte `json:"encryptedkey"` // KMS-encrypted 32-byte data key
}

type Resource struct {
    URN     string                     `json:"urn"`
    Type    string                     `json:"type"`
    Outputs map[string]json.RawMessage `json:"outputs"`
}

// Secret marker — any JSON object containing this key is a secret
const SecretSig = "4dabf18193072939515e22adb298388d"
const SecretSigValue = "1b47061264138c4ac30d75fd1eb44270"

type SecretValue struct {
    Sig        string `json:"4dabf18193072939515e22adb298388d"`
    Ciphertext string `json:"ciphertext,omitempty"`
    Plaintext  string `json:"plaintext,omitempty"`
}
```

### Config file (`Pulumi.<stack>.yaml`)

```go
type StackConfig struct {
    SecretsProvider string                 `yaml:"secretsprovider,omitempty"`
    EncryptedKey    string                 `yaml:"encryptedkey,omitempty"`
    Config          map[string]ConfigValue `yaml:"config,omitempty"`
}

// ConfigValue is either a plain string or {secure: "v1:..."}
type ConfigValue struct {
    Plain  string
    Secure string // set when encrypted
}
```

---

## Crypto

### Data key decryption (KMS)

```go
// KMS URL comes from state: e.g. "awskms://alias/my-key?region=ap-southeast-2&awssdk=v2"
// EncryptedKey in state is raw bytes (not base64 — already decoded from the JSON []byte field)
keeper, _ := gosecrets.OpenKeeper(ctx, kmsURL)
plaintextDataKey, _ := keeper.Decrypt(ctx, encryptedKeyBytes)
// plaintextDataKey is 32 bytes
```

### Secret decryption (AES-256-GCM)

Ciphertext format: `v1:<base64-nonce>:<base64-ciphertext>`

```go
func Decrypt(key []byte, ciphertext string) (string, error) {
    // 1. Split on ":" → ["v1", nonce_b64, ct_b64]
    // 2. base64.StdEncoding.Decode both parts
    // 3. aes.NewCipher(key) → cipher.NewGCM → gcm.Open(nil, nonce, ct, nil)
    // 4. result is JSON-encoded → json.Unmarshal to get actual value
}
```

The decrypted bytes are a JSON-encoded value (string, number, etc.) — unmarshal before returning.

---

## TUI behaviour

- Built with `charmbracelet/bubbletea` + `charmbracelet/bubbles` list component
- Two-screen flow: **stack select** → **secret select**
- `↑`/`↓` or `j`/`k` to navigate
- `Enter` to select
- `Esc` / `b` to go back (from secret list to stack list)
- `q` / `ctrl+c` to quit
- On secret selected: program exits, prints plaintext to stdout
- Secrets labelled: `[output] name` or `[config] project:name`

---

## Error handling

- Missing `--bucket` or `--project` → print usage, exit 1
- S3 read failure → stderr + exit 1
- KMS decrypt failure → stderr + exit 1 (likely auth issue)
- Secret not found → stderr `"secret not found: <path>"` + exit 1
- State has no `secrets_providers` → stderr `"stack has no secrets provider"` + exit 1

---

## Todo

- [ ] **internal/state/types.go** — Pulumi state + config structs
- [ ] **internal/state/secrets.go** — `IsSecret()`, `ExtractSecrets()` from resources + config
- [ ] **internal/store/s3.go** — `ListStacks()`, `ReadState()`, `ReadConfig()` using `gocloud.dev/blob/s3blob`
- [ ] **internal/crypto/decrypt.go** — `NewDecryptor(kmsURL, encryptedKey)`, `Decrypt(ciphertext)`
- [ ] **internal/tui/model.go** — bubbletea model: stack list → secret list → output + quit
- [ ] **internal/tui/styles.go** — lipgloss styling
- [ ] **main.go** — flag parsing (`--bucket`, `--project`, `--region`, `--profile`), dispatch to TUI or direct get
- [ ] **Build verification** — `go build ./...` clean
