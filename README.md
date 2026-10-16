# Loom

An agent-first web framework for Go. Most Loom code is written by AI
agents and reviewed by humans, so Loom is built for that:

- **Strong guarantees.** Templates, components, styles, events and the
  seams between them are checked; mistakes are caught by `loom check`, `go build`,
  startup or tests, each with a stable code, the location and the fix.
- **Built for agents.** There is one way to do each thing, the docs fit
  in an agent's context, and habits from other frameworks that don't
  apply are compile errors that say what to write instead.
- **First-class tooling.** Every CLI command has a `--json` twin, and
  `loom mcp` serves them as tools: routes, sessions, errors, the
  database schema, jobs.
- **Features humans can review.** Component previews and screenshots,
  a dashboard for jobs, and `loom.manifest`, the app's behaviour
  in one diffable file, let a human judge a feature without reading code.
- **Don't rebuild what exists.** Loom owns what carries a guarantee (its
  template language, codegen, the job registry) and should use existing
  libraries for the rest.

Under that: HEEx-like templates (`.hx`) compiled to Go, live server
rendering with minimal diffs over SSE, pages that re-render when the
tables they read change, and events that survive a server restart
without losing the user's work. Postgres for data, durable background
jobs, file storage, sign-in and a jobs dashboard come with it.

Status: pre-1.0. The API can still change between releases.

## Quick start

You need Go 1.25 or later.

```
go install github.com/moomerman/loom/cmd/loom@latest
loom new myapp
cd myapp
loom dev
```

Open http://localhost:4000. See
[docs/getting-started.md](docs/getting-started.md) for what `loom new`
made, including the files that teach an agent to work on the app.

```heex
component TodoList(todos []Todo, filter string)

<ul>
  <li :for={t <- todos} :if={!t.done || filter == "all"} :key={t.id}>
    <input type="checkbox" checked={t.done} on:change="toggle" val-id={t.id} />
    {t.title}
  </li>
</ul>
```

## Docs

Start with [llms.txt](llms.txt) for the docs in reading order; `loom
docs` shows the same from anywhere. The examples in `examples/` are
complete apps: a todo list, a Linear-style issue tracker and a
shared-expenses app, and `jobsweb/` is the reference for idiomatic Loom.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security issues as
[SECURITY.md](SECURITY.md) describes.

## License

[MIT](LICENSE).
