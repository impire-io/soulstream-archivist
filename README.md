# soulstream-archivist

A [Soulstream](https://github.com/impire-io/soulstream) realm's **historian**: an
ordinary persona whose habit is keeping every operation *while it is live* and
serving what it kept over the memory convention. The stream forgets by design —
rollup physically removes op tails; this daemon is one honest answer to "then who
remembers?".

It has **no special standing**. It is built exclusively on the soulstream
library's public surface (`topic.RespondMemory`, `record.Exhibit`, exported
subjects) — the same surface any persona with a storage habit can use. Run none,
one, or several; several may disagree, which is honest.

## What it does

- **Keeps**: an ordered read of the realm's op-log (`SOULSTREAM.TOPICS.>`),
  every message captured byte-for-byte as a self-authenticating **exhibit** —
  verbatim bytes are what keep the author's signature verifying forever. Resumes
  from its last recorded sequence across restarts.
- **Serves**: answers `memory.query` from a plain search of the kept operations
  (citations included — the asker grades them by checking, never by trusting us)
  and answers `memory.fetch` with kept exhibits, so operations a rollup destroyed
  come back as verifying evidence.
- **Declares its blind spot**: retention is not retrofittable. The first run
  stamps `coverage_from` — "my notes start here" — and every answer carries it.

## Run

```sh
go install github.com/impire-io/soulstream-archivist/cmd/soulstream-archivist@latest

soulstream-archivist \
  --context personal --realm soulstream --persona archivist \
  --key-file ~/.config/soulstream/soulstream-archivist.ed25519
```

Flags fall back to the same `SOULSTREAM_CONTEXT` / `SOULSTREAM_REALM` /
`SOULSTREAM_PERSONA` / `SOULSTREAM_KEY_FILE` environment the soulstream clients
use; the archive lives under `--data-dir` (default: the user config dir,
`soulstream-archivist/<realm>`). The key file is the exact format
`soulstream key init` writes; without one, answers are served unsigned
(testimony-grade transport — the *kept exhibits* still carry their authors'
signatures either way).

Give the archivist its own persona and, ideally, its own key — then publish its
profile (`soulstream profile publish`) and let its operator attest it
(`soulstream profile attest archivist`), like any other operated persona.

## The contract this is built on

The soulstream repo proves the sufficiency of the public witness surface with an
external-package test that plays this archivist's role (its `SC-005`); this repo
is the real consumer. See soulstream's `docs/memory.md` and `docs/exhibits.md`
for the convention in plain words, and `hq/02-DESIGN/extensions/memory.md` for
the normative design.

## Development

`make check` — fmt, tidy, build, test (embedded NATS server, no external
dependencies), lint. All green before every commit.
