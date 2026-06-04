# Mantle — Bug Tracker

Bug tracker for **Mantle** (`github.com/inovacc/logger`). Track confirmed defects here. Open a row when a bug is reproduced; move it to a closed state once a fix lands and reference the release in **Fixed-in**.

No bugs are currently open. All packages (`pkg/logger`, `pkg/obsv`, `pkg/bootstrap`, `cmd/logger`) build cleanly, pass `go vet ./...`, and pass `go test ./... -race`.

## Open / Closed bugs

| ID | Severity | Component | Description | Status | Fixed-in |
|----|----------|-----------|-------------|--------|----------|
| —  | —        | —         | No bugs currently tracked. | —      | —        |

## Conventions

- **ID**: Sequential identifier, e.g. `BUG-001`.
- **Severity**: `critical` | `high` | `medium` | `low`.
- **Component**: One of `pkg/logger`, `pkg/obsv`, `pkg/bootstrap`, `cmd/logger`.
- **Description**: One-line summary of the observed defect and how to reproduce.
- **Status**: `open` | `in-progress` | `fixed` | `wontfix`.
- **Fixed-in**: Release tag the fix shipped in, e.g. `v0.1.1` (blank while open).

> Note: Documented design trade-offs (tag-only PII detection, length-revealing masks,
> 64-bit correlation hashes, single-config-per-process, yaml/yml/json-only loading, etc.)
> are tracked as **known limitations** in code and `docs`, not as bugs.
