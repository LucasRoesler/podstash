

## Prerequisites

- **Go 1.26+**
- **[Task](https://taskfile.dev)** — task runner (`go install github.com/go-task/task/v3/cmd/task@latest`)

## Common Tasks

### After writing a new code

Confirm that linting and tests pass, using

```
task check
```


### Build and Run

To quickly start the local server for manual testing, use

```
task build run
```


## Build & Development Commands

Uses [Task](https://taskfile.dev) as the build system:

```bash
task check          # Run fmt + lint + test (full validation)
task test           # Run all tests (no cache)
task test:verbose   # Run tests with verbose output
task lint           # go vet + gofmt check
task fmt            # Format code
task build          # Build binary
task run            # Run locally (DATA_DIR=./temp/data, PORT=8080)
task docker:build   # Build Docker image
task docker:run     # Run Docker container locally
task clean          # Remove binary and temp data
```

Run a single test:
```bash
go test -run TestFunctionName ./pkg/podstash/
```


### Code Standards

- Run `task fmt` before committing — `gofmt` + `goimports`
- Run `task lint` to check for issues
- Follow the patterns in `conventions/go.md` (logging, error handling)
- Tests and linting must always pass, always fix issues that occur.
- Add new tests for new features


## Development Flow

### Commit Format

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add --headless flag to start command
fix: prevent daemon from crashing on stale PID file
refactor: extract session validation into shared helper
```

Explain *why* in the commit message, not just *what*.

### Pull Requests
Ensure that the branch is up-to-date with `main` then open a Pull Request and include a clear description of the purpose of the change. Don't just list what changed, but explain why.
