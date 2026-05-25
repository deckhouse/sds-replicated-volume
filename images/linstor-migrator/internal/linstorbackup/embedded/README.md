# Embedded `linstor-viewer` binary

The file `linstor-viewer` is produced at image/build time and embedded into the migrator binary via `go:embed`. Do not commit the binary (see `.gitignore`).

## Werf / CI

`images/linstor-migrator/werf.inc.yaml` builds the viewer first, copies it here, then builds the migrator so `go:embed` succeeds.

## Local development

From the repository root:

```bash
cd images/linstor-migrator
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o internal/linstorbackup/embedded/linstor-viewer ./cmd/linstor-viewer
chmod 0755 internal/linstorbackup/embedded/linstor-viewer
```

Then build or test the migrator as usual, for example:

```bash
go test ./...
go build -o /tmp/linstor-migrator ./cmd
```

Without `embedded/linstor-viewer`, `go build` of the migrator fails on the embed directive.
