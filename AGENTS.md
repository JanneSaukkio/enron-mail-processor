# Go Agent Guide

## Specific rules for this project

- Ignore files in the `/docs` folder.

## Operating Rules

- Default to standard library and idiomatic patterns before adding dependencies; vet any new library (maintenance, license, API stability) via Awesome Go or upstream docs.
- Always format (`gofmt`/`goimports`) and lint (`go vet`, `golangci-lint`) before shipping. Run tests with `go test` and `-race` when concurrency is involved.
- Keep architecture clean: business logic does not depend on frameworks, databases, or transport types. Wire dependencies at the edges.
- Prefer clarity over cleverness; small, focused functions; early returns on errors.

## Effective Go Essentials

- **Formatting:** `gofmt` is canonical; keep imports grouped stdlib/third-party/local.
- **Comments:** Full sentences starting with the name being described; explain why, not just what.
- **Names:** Package names are short and lower case. Avoid stutter (`bytes.Buffer`). Use common initialisms (`ID`, `HTTP`).
- **Declarations:** Use `:=` for locals; let zero values work; composite literals with field names when many fields.
- **Control:** Prefer `switch` over long `if/else`; range over slices/maps. Watch range variable capture when spawning goroutines.
- **Functions:** Return multiple values instead of out parameters. Keep receiver choice intentional (pointer to mutate or avoid copies; value otherwise).
- **Interfaces:** Define small, behavior-first interfaces at the consumption site. Accept interfaces, return concrete types. Interface satisfaction is implicit.
- **Errors:** Return errors as last value; wrap with `%w` for context; avoid panics for expected problems.
- **Concurrency:** Share memory by communicating. Use channels for ownership transfer or signaling; use mutexes for shared state. Close channels from senders only. Tie goroutines to a lifecycle (context or done channel).
- **Packages:** Export only what callers need; organize code so package names express the domain; avoid cyclic imports.
- **Examples:** Prefer table-driven tests; show simple usage in Godoc examples.

## Uber Go Style Highlights

- Keep APIs minimal and stable; document breaking changes.
- Avoid globals; inject dependencies explicitly.
- Prefer explicit nil checks and short-lived variables; use `var buf bytes.Buffer` instead of pointers when zero value suffices.
- Use functional options or config structs when constructors need flexibility.
- Avoid shadowing important variables (especially `err` across scopes that matter).
- Use typed errors or sentinel errors only when callers must branch; otherwise wrap and return.
- For concurrency, manage lifecycles carefully; avoid unbounded goroutine creation; prefer worker pools or bounded semaphores for bulk work.
- Keep logging structured; avoid logging and returning the same error at multiple layers.

# Tooling & Quality

- Always `run gofmt/goimports` before committing.
- Use `go test` with `-race` for concurrent code paths.
- Keep CI fast; cache module downloads; avoid flaky tests.

# Library Discovery

- Start with the standard library. If a dependency is needed, search Awesome Go by category, then evaluate: maintenance cadence, documentation, test coverage, API clarity, license. Prefer stable, well-maintained options.

Source: <https://raw.githubusercontent.com/waler4ik/golang-ai-agents-md>
