# AGENTS.md — media-scanner

MuxCore sidecar module (`media-scanner`). Workspace deploy and SSH: [`../AGENTS.md`](../AGENTS.md). Default ports: [`_mvp/PORTS.md`](../_mvp/PORTS.md).

## Module identity

| Field | Value |
|-------|-------|
| Directory | `media-scanner` |
| Capabilities | `media.scanner`, `settings` |
| Contracts | `github.com/Muxcore-Media/contracts-scanner` — `muxcore.scanner.v1.ScannerService` |

## Agent rules

- Modules run as gRPC sidecars; capabilities are the security boundary.
- TLS required in production (`MUXCORE_INSECURE_DISABLE_TLS` is dev-only).
- Match existing Go patterns; run `gofmt` and package tests before finishing.
- Cross-module events: use `github.com/Muxcore-Media/contracts-media/events` (`FileImportedPayload`, `ImportFailedPayload`) — not deprecated `core/pkg/contracts` map publishes.
- Canonical protobuf lives in `../contracts-scanner`; `proto/scannerv1` is deprecated.
- Do not edit polluted workspace dumps (see `MASTER-ROADMAP.md` Appendix H).

## Build

```bash
cd media-scanner
nix-shell -p go --run 'export GOCACHE=/tmp/gocache-media-scanner; go test -race ./...'
```
