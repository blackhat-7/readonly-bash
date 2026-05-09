# readonly-bash architecture

## Goal

Approve only commands that are proven read-only, then execute the exact approved command through a hardened runner.

Everything else returns `ask` to the host harness.

## Core concepts

- **Host adapter**: harness-specific glue; passes `requestID`, `cwd`, `command`, runner path, config paths, and guard constraints.
- **Classifier**: parses one shell command string and returns `readonly` or `ask`.
- **Prepare**: classifier + guard checks + approval creation.
- **Approval store**: single-use, locked, cwd-bound approval files.
- **Runner**: no-arg binary mode that claims one approval and executes it under hardened env/shell rules.

## Prepare flow

```mermaid
flowchart TD
  A[Host adapter receives bash command] --> B[Build PrepareRequest]
  B --> C[readonly-bash prepare]
  C --> D{Host guard OK?}
  D -- no --> Z[Return ask]
  D -- yes --> E{Parse command safely?}
  E -- no --> Z
  E -- yes --> F{Every segment allowed read-only?}
  F -- no --> Z
  F -- yes --> G[Normalize commandToRun]
  G --> H{Approval store available?}
  H -- no: active approval exists --> Z
  H -- yes --> I[Create single-use approval]
  I --> J[Return rewrite to exact runner command]
```

## Runner flow

```mermaid
flowchart TD
  A[Host shell runs exact no-arg runner] --> B[Load baked default config]
  B --> C{No args and config valid?}
  C -- no --> X[Fail closed]
  C -- yes --> D[Canonicalize process cwd]
  D --> E[Lock approval store]
  E --> F{Matching unexpired approval for cwd?}
  F -- no --> X
  F -- yes --> G[Atomically claim/delete approval]
  G --> H[Build sanitized env]
  H --> I[Apply Git hardening]
  I --> J[Run trusted shell]
  J --> K[set -f; set +B; commandToRun]
  K --> L[Mirror stdout/stderr/exit]
```

## Approval lifecycle

```mermaid
stateDiagram-v2
  [*] --> None
  None --> Pending: prepare creates approval
  Pending --> Claimed: runner claims under lock
  Pending --> Expired: stale cleanup after TTL
  Claimed --> None: approval removed before execution
  Expired --> None: cleanup removes file
```

## Decisions

- Default result is always `ask`.
- The runner is allowed by hosts as one exact no-arg command.
- No wildcard runner permission.
- Runner config is loaded from a baked default config path.
- `requestID` is diagnostic; runner matching is by locked approval + canonical cwd.
- Approval TTL is only crash cleanup, not concurrency.
- Unknown commands, flags, shell syntax, network tools, mutations, and parse failures are not approved.

## Safety gates

```mermaid
flowchart LR
  A[Original command] --> B[Guard checks]
  B --> C[Shell parser restrictions]
  C --> D[Command allowlist]
  D --> E[Flag/operand validators]
  E --> F[Normalization + quoting]
  F --> G[Single-flight approval store]
  G --> H[Hardened runner]
```

## Host responsibilities

- Call `prepare` before the host permission system.
- Rewrite only on `{ action: "rewrite" }`.
- Leave command unchanged on `{ action: "ask" }`, errors, invalid JSON, or timeout.
- Block direct runner invocation attempts before permission matching.
- Pass accurate guard constraints: shell path, shell prefix, dangerous env, trusted paths.

## Core responsibilities

- Never assume a specific host.
- Parse and classify commands deterministically.
- Create approvals atomically and with strict permissions.
- Canonicalize cwd before approval matching.
- Refuse concurrent unclaimed approvals.
- Sanitize env and enforce trusted shell/PATH at execution time.
