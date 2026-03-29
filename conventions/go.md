# Go Coding Conventions

This document defines coding conventions for the browser-debugger-cli Go codebase (Go 1.26+). Always prefer modern Go idioms over legacy patterns.

## Modern Go Idioms

This project targets **Go 1.26**. Use all modern features up to and including this version. Never use outdated patterns when a modern alternative exists.

### Built-in Functions

Use built-in `min`, `max`, `clear`, and the extended `new`:

```go
// min/max instead of if/else (1.21+)
smallest := min(a, b)
largest := max(a, b, c)

// clear maps and slices (1.21+)
clear(m) // delete all map entries
clear(s) // zero all slice elements

// new(val) instead of temporary variable + address-of (1.26+)
cfg := Config{
    Timeout: new(30),   // *int
    Debug:   new(true), // *bool
}
// NOT:
// timeout := 30
// cfg := Config{Timeout: &timeout}
```

### Type Aliases

Use `any` instead of `interface{}`:

```go
// Good
func process(v any) { ... }

// Bad
func process(v interface{}) { ... }
```

### String and Byte Operations

Prefer `Cut`, `CutPrefix`, `CutSuffix` over `Index` + slice, and `SplitSeq`/`FieldsSeq` over `Split`/`Fields` when iterating:

```go
// strings.Cut instead of Index+slice (1.18+)
before, after, found := strings.Cut(s, ":")

// CutPrefix/CutSuffix (1.20+)
if rest, ok := strings.CutPrefix(s, "http://"); ok {
    // use rest
}

// SplitSeq when iterating (1.24+) — avoids allocating a slice
for part := range strings.SplitSeq(s, ",") {
    process(part)
}
// Also: strings.FieldsSeq, bytes.SplitSeq, bytes.FieldsSeq

// strings.Clone to copy without sharing memory (1.20+)
owned := strings.Clone(s)
```

Use `fmt.Appendf` for building byte buffers:

```go
// Good (1.19+)
buf = fmt.Appendf(buf, "x=%d", x)

// Bad
buf = append(buf, []byte(fmt.Sprintf("x=%d", x))...)
```

### Slices Package

Use `slices` instead of manual loops and `sort.Slice`:

```go
import "slices"

// Membership check
if slices.Contains(validMethods, method) { ... }

// Find index
idx := slices.Index(items, target)
idx := slices.IndexFunc(items, func(item T) bool { return item.ID == id })

// Sorting
slices.Sort(items)
slices.SortFunc(items, func(a, b T) int { return cmp.Compare(a.X, b.X) })

// Aggregation
biggest := slices.Max(items)

// Mutation
slices.Reverse(items)
slices.Compact(items) // remove consecutive duplicates

// Copying
clone := slices.Clone(s)

// Iterators (1.23+)
keys := slices.Collect(maps.Keys(m))
sortedKeys := slices.Sorted(maps.Keys(m))
```

### Maps Package

Use `maps` instead of manual map iteration:

```go
import "maps"

clone := maps.Clone(m)
maps.Copy(dst, src)
maps.DeleteFunc(m, func(k K, v V) bool { return condition })

// Iterators (1.23+)
for k := range maps.Keys(m) { process(k) }
for v := range maps.Values(m) { process(v) }
```

### Cmp Package

Use `cmp.Or` for defaults and `cmp.Compare` for ordering:

```go
import "cmp"

// First non-zero value (1.22+)
name := cmp.Or(os.Getenv("NAME"), "default")
port := cmp.Or(config.Port, 9222)

// Comparison for sorting
slices.SortFunc(items, func(a, b T) int {
    return cmp.Compare(a.Priority, b.Priority)
})
```

### Loop Patterns

Use modern loop syntax:

```go
// Range over integer (1.22+)
for i := range len(items) {
    process(items[i])
}
// NOT: for i := 0; i < len(items); i++

// Loop variables are safe to capture in goroutines (1.22+)
for _, item := range items {
    go func() {
        process(item) // safe, each iteration has its own copy
    }()
}
```

### Concurrency

Use modern sync and atomic patterns:

```go
// WaitGroup.Go (1.25+)
var wg sync.WaitGroup
for _, item := range items {
    wg.Go(func() {
        process(item)
    })
}
wg.Wait()
// NOT: wg.Add(1) + go func() { defer wg.Done(); ... }()

// sync.OnceFunc / OnceValue (1.21+)
initOnce := sync.OnceFunc(func() { expensiveInit() })
getValue := sync.OnceValue(func() Config { return loadConfig() })

// Type-safe atomics (1.19+)
var flag atomic.Bool
flag.Store(true)
if flag.Load() { ... }

var ptr atomic.Pointer[Config]
ptr.Store(cfg)
```

### Context

Use cause-aware context functions:

```go
// WithCancelCause (1.20+)
ctx, cancel := context.WithCancelCause(parent)
cancel(fmt.Errorf("shutdown requested"))

// Retrieve the cause
if err := context.Cause(ctx); err != nil { ... }

// WithTimeoutCause / WithDeadlineCause (1.21+)
ctx, cancel := context.WithTimeoutCause(parent, 5*time.Second,
    fmt.Errorf("CDP command timed out"))

// AfterFunc — run cleanup on cancellation (1.21+)
stop := context.AfterFunc(ctx, cleanup)
defer stop()
```

### Time

```go
// Good
elapsed := time.Since(start)
remaining := time.Until(deadline)

// Bad
elapsed := time.Now().Sub(start)
remaining := deadline.Sub(time.Now())
```

### JSON Struct Tags

Use `omitzero` for zero-value omission (1.24+):

```go
type Config struct {
    Timeout time.Duration `json:"timeout,omitzero"`
    Created time.Time     `json:"created,omitzero"`
    Items   []string      `json:"items,omitzero"`
}
// NOT: omitempty (which doesn't work correctly for Duration, Time, structs)
```

### HTTP Routing

Use enhanced `http.ServeMux` patterns (1.22+):

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    // ...
})
```

### Reflection

```go
// Good (1.22+)
t := reflect.TypeFor[MyStruct]()

// Bad
t := reflect.TypeOf((*MyStruct)(nil)).Elem()
```

### Testing

```go
// Use t.Context() for test contexts (1.24+)
func TestFoo(t *testing.T) {
    ctx := t.Context()
    result := doSomething(ctx)
}
// NOT: ctx, cancel := context.WithCancel(context.Background()); defer cancel()

// Use b.Loop() for benchmarks (1.24+)
func BenchmarkFoo(b *testing.B) {
    for b.Loop() {
        doWork()
    }
}
// NOT: for i := 0; i < b.N; i++
```

---

## Error Handling

### Use Error Wrapping

Always wrap errors with context using `fmt.Errorf` with `%w`:

```go
// Good - preserves error chain
if err := w.cdpClient.EnableNetwork(ctx); err != nil {
    return fmt.Errorf("failed to enable network monitoring: %w", err)
}

// Bad - loses original error
if err := w.cdpClient.EnableNetwork(ctx); err != nil {
    return fmt.Errorf("failed to enable network monitoring: %v", err)
}
```

### Error Inspection

Use `errors.Is` for sentinel checks. Use `errors.AsType` (1.26+) for typed error extraction:

```go
// Sentinel check (1.13+)
if errors.Is(err, ErrNoSession) {
    // handle
}

// Combine errors (1.20+)
return errors.Join(err1, err2)

// Type extraction — use errors.AsType (1.26+)
if pathErr, ok := errors.AsType[*os.PathError](err); ok {
    handle(pathErr)
}
// NOT:
// var pathErr *os.PathError
// if errors.As(err, &pathErr) { ... }
```

### Domain-Specific Errors

Define domain-specific error sentinels for important error conditions:

```go
// Define sentinels
var (
    ErrNoSession      = errors.New("no active session")
    ErrStaleSession   = errors.New("stale session files")
    ErrDaemonNotAlive = errors.New("daemon not running")
)

// Wrap with additional context
if !FileExists(DaemonPidFile()) {
    return 0, ErrNoSession
}

if err := validatePID(pid); err != nil {
    return 0, fmt.Errorf("%w: %v", ErrStaleSession, err)
}
```

### Error Checking Patterns

**Check every error** — never silently ignore errors:

```go
// Good - check and handle
data, err := json.MarshalIndent(v, "", "  ")
if err != nil {
    return cli.Errorf(cli.ExitSoftwareError, "failed to marshal JSON: %v", err)
}

// Only ignore errors when documented
// Ignore error - best effort cleanup, process may already be dead
_ = process.ForceKillChromeProcess(pid)
```

**Document intentionally ignored errors**:

```go
// Good - explicit comment explaining why
// Ignore error - we're already in error path, best effort cleanup
_ = launchedChrome.Kill()

// Bad - silent ignore with no explanation
_ = launchedChrome.Kill()
```

### Error Return Patterns

Return errors, don't log and continue:

```go
// Good - return error
if err := w.navigateToURL(ctx, url); err != nil {
    return fmt.Errorf("failed to navigate: %w", err)
}

// Bad - log and continue (error is lost)
if err := w.navigateToURL(ctx, url); err != nil {
    logger.Error("navigation failed", "error", err)
    // Continues anyway - error is silently ignored!
}
```

### Error Types

Define custom error types for structured error information:

```go
type staleCacheError struct {
    message string
}

func (e *staleCacheError) Error() string {
    return e.message
}

// Type checking with errors.AsType (1.26+)
if cacheErr, ok := errors.AsType[*staleCacheError](err); ok {
    // Handle stale cache specifically
}
```

### CLI Error Handling

Use structured exit errors for CLI commands:

```go
const (
    ExitSuccess          = 0
    ExitInvalidArguments = 81
    ExitResourceNotFound = 83
    ExitSoftwareError    = 110
)

if !sessionExists() {
    return cli.NewExitError("No active session\nStart with: bdg <url>",
        cli.ExitResourceNotFound)
}

if errors.Is(err, session.ErrNoSession) {
    return cli.NewExitError("No active session", cli.ExitResourceNotFound)
}
```

### JSON Error Handling

For CLI commands supporting `--json`, handle errors appropriately:

```go
if err != nil {
    if jsonOutput {
        if encodeErr := ui.ErrorJSON(err.Error(), exitCode, suggestion); encodeErr != nil {
            return cli.Errorf(cli.ExitSoftwareError, "failed to encode JSON: %v", encodeErr)
        }
        return cli.NewExitError("", exitCode)
    }
    return cli.NewExitError(err.Error(), exitCode)
}
```

---

## Logging

### Use Structured Logging (log/slog)

**Always use `log/slog` for logging, never the standard `log` package.**

```go
import "log/slog"

// Good
logger.Info("session started", "url", url, "pid", pid)
logger.Warn("cache save failed", "error", err, "selector", selector)
logger.Error("CDP connection failed", "error", err, "wsURL", wsURL)

// Bad - don't use standard log package
import "log"
log.Printf("session started: %s", url)  // NO
```

### Log Levels

Choose appropriate log levels:

- **Debug**: Detailed diagnostic information for troubleshooting
  ```go
  logger.Debug("CDP message received", "method", method, "params", params)
  ```

- **Info**: General informational messages about normal operation
  ```go
  logger.Info("session started", "url", url, "pid", daemonPID)
  logger.Info("telemetry initialized", "enabled", count)
  ```

- **Warn**: Non-critical issues that don't prevent operation
  ```go
  logger.Warn("cache save failed", "error", err, "selector", selector)
  logger.Warn("network did not stabilize", "error", err)
  ```

- **Error**: Critical failures that prevent normal operation
  ```go
  logger.Error("CDP connection failed", "error", err, "wsURL", wsURL)
  logger.Error("failed to launch Chrome", "error", err)
  ```

### Structured Context

Always include relevant context as key-value pairs:

```go
// Good - structured fields
logger.Info("DOM element clicked",
    "selector", selector,
    "index", index,
    "clickable", isClickable)
a
// Bad - unstructured string formatting
logger.Info(fmt.Sprintf("Clicked element %s at index %d", selector, index))
```

### Logger Initialization

Create loggers with appropriate defaults:

```go
opts := &slog.HandlerOptions{
    Level: slog.LevelInfo,
}

if debug {
    opts.Level = slog.LevelDebug
}

handler := slog.NewTextHandler(os.Stderr, opts)
logger := slog.New(handler)
```

### Component Context

Add component context when creating logger instances:

```go
w.logger = slog.New(handler).With("component", "daemon")
w.logger.Info("starting") // Logs: component=daemon msg="starting"
```

---

## Quick Reference

### Modern Go Idioms

- Use `any` not `interface{}`
- Use `min`/`max` not if/else comparisons
- Use `new(val)` not temporary variable + `&x`
- Use `cmp.Or` for default values
- Use `slices` and `maps` packages not manual loops
- Use `strings.Cut`/`CutPrefix`/`CutSuffix` not `Index` + slice
- Use `strings.SplitSeq` not `strings.Split` when iterating
- Use `errors.AsType[T]` not `errors.As` with pointer variable
- Use `omitzero` not `omitempty` for `time.Duration`, `time.Time`, structs, slices, maps
- Use `for i := range n` not `for i := 0; i < n; i++`
- Use `wg.Go(fn)` not `wg.Add(1)` + `go func() { defer wg.Done() ... }()`
- Use `t.Context()` not `context.WithCancel(context.Background())` in tests
- Use `b.Loop()` not `for i := 0; i < b.N; i++` in benchmarks

### Logging

- Use `log/slog` exclusively
- Choose appropriate log levels (Debug/Info/Warn/Error)
- Use structured fields, not string formatting
- Add component context to loggers
- CLI: minimize logging, daemon: log operations

### Error Handling

- Wrap errors with `%w` to preserve chain
- Use `errors.AsType[T]` for typed extraction
- Use `errors.Join` to combine multiple errors
- Define domain-specific error sentinels
- Check every error — never silently ignore
- Document intentionally ignored errors with comments
- Return errors, don't log and continue
- Use structured exit errors for CLI commands
- Map domain errors to appropriate exit codes
