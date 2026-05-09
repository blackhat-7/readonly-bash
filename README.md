# readonly-bash

Small Go library/CLI for classifying and running proven read-only shell commands.

## What it does

- Classifies a narrow allowlist of read-only bash commands.
- Returns `ask` for unknown, mutating, network-capable, or hard-to-parse commands.
- Creates single-use approvals for safe commands.
- Runs approved commands through a hardened no-arg runner.

## CLI

```bash
readonly-bash classify < request.json
readonly-bash prepare < request.json
readonly-bash run --config ./readonly-bash.json
```

`readonly-bash-runner` is the no-arg runner mode, intended for host integrations that need one exact allowlisted command.

## Nix

```nix
inputs.readonly-bash.url = "github:blackhat-7/readonly-bash";
```

Use `inputs.readonly-bash.lib.mkPackage { inherit pkgs; defaultConfigPath = "/path/to/config.json"; }` to bake a default runner config path.
