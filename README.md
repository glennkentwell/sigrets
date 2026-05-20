# sigrets :cigarette:

Read Pulumi secrets from S3 state without the Pulumi CLI.

## Install

```bash
go install glenn.io/sigrets@latest
```

## Usage

### Interactive TUI

```bash
sigrets
```

Browse projects → stacks → secrets. Secrets are obfuscated until you press `space` to reveal. Press `y` to copy to clipboard, `enter` to print and exit, `esc` to go back.

### Direct get

```bash
sigrets <project> <stack>.<src>.<name>
```

`<project>` is fuzzy-matched — a substring of the project path is enough.

| source | what | example |
|--------|------|---------|
| `o` | stack output (exported value) | `staging.o.databaseUrl` |
| `c` | config secret (`pulumi config set --secret`) | `staging.c.aws:accessKey` |

```bash
# config secret — fuzzy project match ('prod' → 'cloud/production')
sigrets prod staging.c.stripe:secretKey

# stack output secret
sigrets prod staging.o.dbConnectionString

# full project path also works
sigrets cloud/production staging.c.github:token
```

The matched project is printed to stderr so you always know what was resolved:

```
using project: cloud/production
sk_live_••••••••••••••••••••••••••••••••
```

## Configuration

On first run, sigrets prompts for your S3 bucket name and saves it to `~/.config/sigrets.json`. You can also set it via:

| method | example |
|--------|---------|
| env var | `SIGRETS_BUCKET=my-pulumi-state sigrets` |
| flag | `sigrets --bucket my-pulumi-state` |
| config file | `~/.config/sigrets.json` |

Other flags:

```
--region   AWS region (default: ap-southeast-2 or AWS_DEFAULT_REGION)
--profile  AWS named profile (default: AWS_PROFILE env)
```

## How it works

Pulumi uses envelope encryption for secrets in S3-backed state:

1. Each stack's state file (`<project>/.pulumi/stacks/<stack>.json`) contains a `secrets_providers.state` with a KMS key URL and a KMS-encrypted 32-byte data key
2. sigrets calls KMS to decrypt the data key
3. Each secret is decrypted locally with AES-256-GCM using the data key

Config secrets (`pulumi config set --secret`) are read from the latest history file at `<project>/.pulumi/history/<stack>/`.

You don't need the full Pulumi CLI or the local Pulumi stack config YAML files, you can just rawdog AWS S3 and KMS :dog:    
