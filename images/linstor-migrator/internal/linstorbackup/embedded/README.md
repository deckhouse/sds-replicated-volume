# Embedded `linstor-viewer` binary

The file `linstor-viewer` is produced at image/build time and embedded into the migrator binary via `go:embed` when building with `-tags embed_linstor_viewer`. Do not commit the binary (see `.gitignore`).

## Werf / production build

`images/linstor-migrator/werf.inc.yaml` builds the viewer first, copies it here, then builds the migrator with `-tags embed_linstor_viewer` so `go:embed` succeeds.

## Unit tests and local `go test`

Tests compile without `embedded/linstor-viewer`: the default build uses an empty stub (`embed_stub.go`). No pre-build step is required for `go test ./...`.

## Local migrator binary (with embedded viewer)

From the repository root:

```bash
cd images/linstor-migrator
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o internal/linstorbackup/embedded/linstor-viewer ./cmd/linstor-viewer
chmod 0755 internal/linstorbackup/embedded/linstor-viewer
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags embed_linstor_viewer -ldflags="-s -w" \
  -o /tmp/linstor-migrator ./cmd
```

Without `embedded/linstor-viewer`, `go build -tags embed_linstor_viewer` fails on the embed directive.
A plain `go build ./cmd` (no tag) produces a migrator without an embedded viewer; backup install will error at runtime if the viewer is required.
