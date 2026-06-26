# Contributing

Thanks for your interest in improving Ambatukam Go.

## Development Setup

Requirements:

- Go 1.21+ (the CI matrix runs against 1.21, 1.22, 1.23)

Clone and verify:

```bash
git clone https://github.com/farhanturu/ambatukam-go.git
cd ambatukam-go
go test ./...
```

## Code Style

- All code must be `gofmt`-clean and `go vet`-clean. CI fails otherwise.
- Follow standard Go conventions and idioms.
- Keep exported API minimal and well-documented.
- Match the patterns already in the codebase.

## Testing

- New features and bug fixes must include tests.
- Run the full suite with the race detector before opening a PR:

```bash
go test -race -timeout 60s -count=1 ./...
```

- Add `// Output:` lines to godoc examples so `go test -run Example ./...`
  can verify their output stays correct.

## Pull Requests

- One logical change per PR.
- Link the related issue if one exists.
- Write a clear PR description explaining the motivation and approach.
- Ensure all CI checks pass.
- Keep diffs focused; avoid drive-by refactors.

## Code of Conduct

Be respectful. Assume good faith. Disagree on ideas, not people.

## Reporting Security Issues

See `SECURITY.md`. Do not file security issues as public GitHub issues.
