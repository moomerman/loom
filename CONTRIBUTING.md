# Contributing

Thanks for helping. Loom is pre-1.0 and the API still moves, so for
anything bigger than a fix, open an issue first to agree the shape.

1. Read [AGENTS.md](AGENTS.md): the commands, the layout, the design
   rules and how to add an error code. It is written for agents and
   people alike; point your coding agent at it.
2. Make the change with tests. New behaviour is tested with `loomtest`;
   a new error has a code, a fix and an entry in `docs/errors.md`; new
   CLI output has `--json`.
3. Run what CI runs: `go vet ./...`, `gofmt -l .`, staticcheck and
   `go test -race ./...`, ideally with `LOOM_TEST_DATABASE_URL` set so
   nothing skips (AGENTS.md, "Commands").
4. Open a pull request saying what changes for someone using Loom.

By contributing you agree that your contribution is licensed under the
[MIT License](LICENSE).
