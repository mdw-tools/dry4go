---
name: eliminate-duplication
description: >
  Find and eliminate duplicate Go code using dry4go, a structural
  (AST-fingerprint) duplicate detector. Use when the user asks to "DRY up",
  deduplicate, find copy-paste, or consolidate repeated logic in a Go
  codebase, or as a cleanup pass after generating or merging a lot of new Go
  code. Not for non-Go code or for textual/diff-based comparison.
allowed-tools: Bash(dry4go:*), Bash(go:*), Bash(make:*), Read, Grep, Glob, Edit, Write
---

# Eliminate Duplicate Go Code with dry4go

dry4go compares every Go function and method in the given files/directories by
normalizing each one to structural fingerprints (identifiers, local names, and
literal values are erased; control flow, shape, and operators are kept). It
reports *candidate* pairs with a similarity score — `1.0` means structurally
identical after normalization. It only finds duplication; you evaluate each
candidate and perform the refactoring.

## Step 1: Ensure dry4go is available

```bash
dry4go --help
```

If not installed:

```bash
go install github.com/mdw-tools/dry4go/cmd/...
```

## Step 2: Scan for candidates

Run from the target repository root, requesting JSON so results are easy to
process:

```bash
dry4go --json .
```

Useful options:

| Option          | Default | Purpose                                          |
|-----------------|---------|--------------------------------------------------|
| `--threshold N` | 0.82    | Minimum similarity score to report               |
| `--min-lines N` | 4       | Ignore functions shorter than N source lines     |
| `--min-nodes N` | 20      | Ignore functions with fewer normalized AST nodes |
| `--json`        | text    | Machine-readable output                          |

Directories are searched recursively (`.git`, `vendor`, and `target` are
skipped). Multiple file/directory arguments all participate in one search set,
so cross-package duplication is found too.

Tuning guidance:

- Start at the default threshold. If the report is empty, retry once at
  `--threshold 0.75` to surface near-duplicates worth reviewing.
- If the report is overwhelming, raise to `--threshold 0.9` and handle the
  strongest matches first.

JSON output shape:

```json
{
  "candidates": [
    {
      "score": 0.89,
      "left":  {"file": "internal/billing/invoice.go", "start_line": 12, "end_line": 25},
      "right": {"file": "internal/billing/receipt.go", "start_line": 30, "end_line": 44},
      "left_nodes": 88,
      "right_nodes": 91
    }
  ]
}
```

## Step 3: Triage each candidate

Sort candidates by score, highest first. For each pair, Read both line ranges
(with a few lines of surrounding context) and decide:

**Consolidate** when the two functions express the same *intent* and would
change together — same algorithm applied to different names, fields, or
constants. A score at or near 1.0 with matching intent is almost always worth
merging.

**Skip** when the duplication is incidental or protective:

- Test code that is deliberately explicit and repetitive (table-driven tests,
  assertion sequences) — readability beats DRY in tests.
- Functions that are structurally similar today but serve different domains
  and would evolve independently (coincidental similarity — merging couples
  things that should stay decoupled).
- Generated code (files marked `// Code generated ... DO NOT EDIT.`).
- Cases where the abstraction needed to unify them would be more complex than
  the duplication it removes.

Record a one-line verdict per candidate so the final report accounts for every
pair, including the skipped ones.

## Step 4: Refactor the consolidations

Pick the lightest technique that removes the duplication:

1. **Extract a shared function** in the package (or an internal shared
   package if the duplicates cross package boundaries), parameterizing the
   parts that differed — the values, names, and predicates dry4go normalized
   away.
2. **Use generics** when the duplicates differ only by type.
3. **Pass a function value** (strategy) when the duplicates differ by one
   embedded behavior.
4. **Extract a method on a shared type** when the duplicates share state.

Work one candidate pair at a time. Follow existing local conventions and run
the project's tests after each consolidation (prefer `make test` if the
target repo has it, otherwise `go test ./...`) — never batch several risky
refactorings between test runs. If tests fail, fix or revert before moving on.

## Step 5: Verify and report

Re-run the same dry4go command from Step 2 and confirm the consolidated
candidates no longer appear. Then summarize for the user:

- Candidates found, consolidated, and skipped (with the reason for each skip).
- The refactoring applied for each consolidation, referenced as
  `file:line`.
- Test results proving behavior is preserved.

Duplication that was found but deliberately left in place is a finding, not a
failure — say so explicitly rather than silently omitting it.
