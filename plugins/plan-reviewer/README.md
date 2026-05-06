# plan-reviewer (plugin)

Browser-based inline review for Claude Code plan mode. See the [repo README](../../README.md) for user-facing install and usage.

This document covers how the plugin is built and laid out.

## How it works

```
ExitPlanMode                            browser
   │                                       ▲
   ▼                                       │  http://127.0.0.1:<ephemeral>
PreToolUse hook ── launch.sh ── binary ────┤
   ▲                             │         │
   │  allow (+ updatedInput      │         ▼
   │  carrying annotated plan    │   select text, comment,
   │  and additionalContext)     │   click Approve | Send
   └─────────────────────────────┤
                                 ▼
                       ExitPlanMode persists
                       updatedInput.plan with
                       `> 💬 FEEDBACK on "…":` annotations
```

- `hooks/hooks.json` registers a `PreToolUse` matcher on `ExitPlanMode`.
- `hooks/launch.sh` picks `bin/<os>-<arch>/plan-reviewer.gz`, decompresses it into `~/.cache/plan-reviewer/` on first use, execs it.
- The binary reads the hook JSON payload from stdin, discovers the plan path from the session transcript (validated to be inside `~/.claude/plans/`), serves the review UI, and blocks until the user submits.
- On approve: emits `permissionDecision: allow`. Claude Code's normal approval flow continues.
- On feedback: emits `permissionDecision: allow` with `updatedInput.plan` set to the plan text annotated with `> 💬 FEEDBACK on "<anchor>":` blockquotes, plus `additionalContext` steering Claude to re-enter plan mode and address the annotations. The hook itself never writes to the plan file — ExitPlanMode persists `updatedInput.plan`, so there's exactly one writer and the plan's mtime stays in sync with Claude's read cache. `allow` + `updatedInput` also suppresses ExitPlanMode's native approve/reject dialog, so the user doesn't see an extra prompt after submitting feedback.

## Layout

```
plugins/plan-reviewer/
├── .claude-plugin/plugin.json
├── hooks/{hooks.json, launch.sh}
├── bin/<os>-<arch>/plan-reviewer.gz     # committed, cross-compiled
├── cmd/plan-reviewer/
│   ├── main.go                          # entry: hook I/O, server lifecycle
│   └── web/{index.html, app.js, style.css}
└── internal/
    ├── hook/         # parse/emit PreToolUse JSON
    ├── planfile/     # discover plan path, apply feedback
    ├── render/       # markdown → HTML
    ├── server/       # localhost HTTP with CSRF
    └── theme/        # read Claude Code theme
```

## Build

Requires Go 1.26+. No external Go dependencies.

```text
make build       # uncompressed binary for the current platform
make build-all   # cross-compile darwin/linux × arm64/amd64, gzip each
make test        # run all tests (go test ./... -race)
make clean       # remove bin/
```

Committed artifacts are only the gzipped binaries under `bin/<os>-<arch>/plan-reviewer.gz`. Uncompressed binaries are `.gitignore`d. The release workflow in `.github/workflows/release.yml` rebuilds on tag push and commits the refreshed `.gz` files back to `main`.

## Security

- Plan file path is constrained to `~/.claude/plans/*.md`; symlinks are resolved and must stay inside that directory.
- The hook never writes to the plan file. Annotated plan text is returned via `updatedInput.plan` and ExitPlanMode persists it, so the symlink-write surface doesn't exist.
- Only the `plan` key is forwarded in `updatedInput`; any extra keys in the incoming `tool_input` (which is ultimately model-generated and could be influenced by prompt injection of the transcript) are dropped.
- `/submit` has a 1 MiB body cap via `http.MaxBytesReader`.
- Markdown link rendering uses a scheme allowlist (`http`, `https`, `mailto`, `#`, `/`, `./`, `../`); `javascript:`, `data:`, `vbscript:`, `file:` are stripped.
- `POST /submit` requires a random CSRF token generated per server start and injected into the HTML.
- `Origin` header is rejected when present and not equal to the server's own origin.
- The plan path and CSRF token are JSON-encoded into the inline `<script>` to prevent `</script>` breakouts.

## License

MIT — see [LICENSE](../../LICENSE).
