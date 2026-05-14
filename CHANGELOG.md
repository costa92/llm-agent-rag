# Changelog

All notable changes to `github.com/costa92/llm-agent-rag` will be documented in
this file.

<!-- Keep a Changelog format: https://keepachangelog.com/en/1.1.0/ -->
<!-- Semver: https://semver.org/ -->

## [v0.1.0] - 2026-05-14

Initial standalone RAG SDK release.

### Added

- standalone Go module: `github.com/costa92/llm-agent-rag`
- abstract import via `ingest.Source`, `Import`, and `ImportFrom`
- deterministic default `ingest.CharSplitter`
- default `embed.HashEmbedder`
- default `store.InMemoryStore`
- abstract generation seam via `generate.Model`
- prompt customization via `prompt.Template`
- `rag.System` orchestration for import, retrieve, ask, remove, and stats
- `advanced` package for:
  - multi-query expansion (`MQE`)
  - HyDE-style hypothetical answer generation
- optional `adapter/llmagent` bridge behind build tag `llmagent`

### Notes

- Core module is intentionally publishable without a hard dependency on
  `github.com/costa92/llm-agent`.
- `adapter/llmagent` is a development bridge and requires a temporary local
  `require` / `replace` when tested in isolation.
- This release is intended as a reusable `v0.1` baseline, not a stability
  guarantee.
