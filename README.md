# Benchmark results

Measurements behind engineering changes at Latere AI. Each folder is one
study: what was measured and how, every raw `go test` line, the statistics,
the figures, and the tools and scripts that produced them.

| Study | What it measured |
|---|---|
| [json-v2-migration](json-v2-migration) | 29 Go repositories moved from `encoding/json` to `encoding/json/v2` on Go 1.27: 762 JSON benchmarks under the classic engine, Go 1.27's default and the migrated code, with the migration's full log |
