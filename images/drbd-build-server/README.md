# DRBD Build Server

A high-performance HTTP server that builds DRBD kernel modules on-demand for various kernel versions. The server accepts kernel headers, builds DRBD modules asynchronously, and caches the results for efficient reuse.

## Overview

The DRBD Build Server is designed to provide a centralized service for building DRBD kernel modules. It eliminates the need to build modules on each node by providing a shared build service that can be accessed across a Kubernetes cluster.

## Features

- **On-Demand Module Building**: Builds DRBD kernel modules for any kernel version
- **Intelligent Caching**: Caches built modules using SHA256 hash of kernel version and headers
- **Asynchronous Builds**: Non-blocking build process with job status tracking
- **Multiple DRBD Source Options**: 
  - Use pre-existing DRBD source directory
  - Clone from Git repository with version specification
- **RESTful API**: Simple HTTP API for build requests and status checks
- **SPAAS Integration**: Integrates with DRBD's SPAAS build service
- **TLS Support**: Optional TLS encryption for secure communication

## Architecture

The server consists of several components:

- **HTTP Server**: Handles incoming build requests and serves cached modules
- **Build Service**: Manages build jobs and coordinates the build process
- **Job Repository**: Tracks build job status and metadata
- **Cache System**: Stores built modules in compressed tar.gz format

## API Endpoints

### POST `/api/v1/build`

Initiates a build request for DRBD kernel modules.

**Request:**
- **Body**: Kernel headers as `tar.gz` archive
- **Headers**: 
  - `X-Kernel-Version` (optional): Kernel version (e.g., `5.15.0-86-generic`)
- **Query Parameters**:
  - `kernel_version` (optional): Kernel version (alternative to header)

**Response:**
- `202 Accepted`: Build job created or already in progress
- `200 OK`: Cached module found, returns module file immediately

**Response Body (JSON):**
```json
{
  "status": "building",
  "job_id": "abc123..."
}
```

### GET `/api/v1/status/{job_id}`

Retrieves the status of a build job.

**Response:**
- `200 OK`: Job completed or failed
- `202 Accepted`: Job still in progress
- `404 Not Found`: Job not found

**Response Body (JSON):**
```json
{
  "status": "completed",
  "job_id": "abc123...",
  "kernel_version": "5.15.0-86-generic",
  "created_at": "2024-01-01T12:00:00Z",
  "completed_at": "2024-01-01T12:05:00Z",
  "duration": "5m0s",
  "cache_path": "/var/cache/drbd-build-server/abc123....tar.gz",
  "download_url": "/api/v1/download/abc123..."
}
```

### GET `/api/v1/download/{job_id}`

Downloads the built DRBD kernel modules.

**Response:**
- `200 OK`: Returns `application/gzip` file with modules
- `400 Bad Request`: Job not completed yet
- `404 Not Found`: Job or cache file not found

### GET `/api/v1/hello`

Health check endpoint.

**Response:**
- `200 OK`: Server is running

## Configuration

### Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:2021` | Server address and port |
| `--drbd-dir` | `/drbd` | Path to DRBD source directory |
| `--drbd-version` | `` | DRBD version to clone (e.g., `9.2.12`) |
| `--drbd-repo` | `https://github.com/LINBIT/drbd.git` | DRBD repository URL |
| `--cache-dir` | `/var/cache/drbd-build-server` | Path to cache directory |
| `--maxbytesbody` | `104857600` | Maximum request body size (100MB) |
| `--keeptmpdir` | `false` | Keep temporary directories for debugging |
| `--certfile` | `` | Path to TLS certificate file |
| `--keyfile` | `` | Path to TLS key file |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--version` | - | Print version and exit |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `SPAAS_URL` | SPAAS service URL (default: `https://spaas.drbd.io`) |
| `DRBD_VERSION` | DRBD version to clone (overrides `--drbd-version`) |
| `DRBD_REPO` | DRBD repository URL (overrides `--drbd-repo`) |

## Build Process

1. **Request Reception**: Server receives kernel headers archive and kernel version
2. **Cache Check**: Generates cache key (SHA256 of kernel version + headers) and checks cache
3. **Job Creation**: If not cached, creates a new build job
4. **DRBD Source Preparation**:
   - If `--drbd-dir` exists: Copies DRBD source to isolated build directory
   - If `--drbd-version` specified: Clones DRBD repository for the specified version
5. **Kernel Headers Extraction**: Extracts kernel headers from request body
6. **Build Execution**: Runs `make module` with kernel build directory
7. **Module Collection**: Collects all `.ko` files from build output
8. **Archive Creation**: Creates `tar.gz` archive with modules
9. **Cache Storage**: Saves archive to cache directory
10. **Status Update**: Updates job status to `completed`

## Caching

The server uses an intelligent caching mechanism:

- **Cache Key**: SHA256 hash of kernel version + kernel headers data
- **Cache Location**: `/var/cache/drbd-build-server/{cache_key}.tar.gz`
- **Cache Hit**: If cache exists and is valid, module is served immediately
- **Cache Miss**: New build job is created

This ensures that identical kernel configurations result in cache hits, significantly reducing build time.

## Job Status

Build jobs can have the following statuses:

- `notExist`: Job does not exist
- `pending`: Job created but not yet started
- `building`: Build in progress
- `completed`: Build completed successfully
- `failed`: Build failed with error

## Usage Examples

### Build Request

```bash
# Send kernel headers and request build
curl -X POST \
  -H "X-Kernel-Version: 5.15.0-86-generic" \
  --data-binary @kernel-headers.tar.gz \
  http://drbd-build-server:2021/api/v1/build
```

### Check Build Status

```bash
# Check job status
curl http://drbd-build-server:2021/api/v1/status/abc123...
```

### Download Built Modules

```bash
# Download completed build
curl -O http://drbd-build-server:2021/api/v1/download/abc123...
```

### Using Query Parameter for Kernel Version

```bash
curl -X POST \
  "http://drbd-build-server:2021/api/v1/build?kernel_version=5.15.0-86-generic" \
  --data-binary @kernel-headers.tar.gz
```

## Deployment

The server is designed to run in Kubernetes and is typically deployed as a Deployment with:

- **Persistent Cache Volume**: For module cache persistence
- **DRBD Source Volume**: Optional, if using pre-existing DRBD source
- **Resource Limits**: CPU and memory limits based on build requirements
- **Health Checks**: Liveness, readiness, and startup probes on `/api/v1/hello`

### Resource Requirements

- **Minimum**: 100m CPU, 512Mi memory
- **Maximum**: 2000m CPU, 4Gi memory (with VPA)
- **Storage**: Depends on cache size and number of kernel versions

## Kernel Version Format

Kernel versions must match the format:
```
X.Y.Z[-flavor] or X.Y.Z[-flavor]-build
```

Examples:
- `5.15.0`
- `5.15.0-generic`
- `5.15.0-86-generic`

## DRBD Source Configuration

The server supports two modes for DRBD source:

1. **Pre-existing Directory**: Mount DRBD source at `--drbd-dir` (default: `/drbd`)
2. **Git Clone**: Specify `--drbd-version` to clone from repository

When cloning, the server:
- Clones the branch `drbd-{version}` (e.g., `drbd-9.2.12`)
- Initializes and updates submodules
- Creates `.drbd_git_revision` file with git hash
- Removes `.git` directory after cloning

## Security Considerations

- **TLS**: Use `--certfile` and `--keyfile` for encrypted communication
- **Request Size Limits**: `--maxbytesbody` limits request body size (default: 100MB)
- **Path Traversal Protection**: Archive extraction validates paths to prevent directory traversal
- **Isolated Builds**: Each build uses an isolated temporary directory

## Troubleshooting

### Enable Debug Logging

```bash
--log-level=debug
```

### Keep Temporary Directories

```bash
--keeptmpdir=true
```

This allows inspection of build artifacts for debugging.

### Check Build Logs

The server logs all build operations with structured logging. Look for:
- `job_id`: Unique identifier for each build
- `kernel_version`: Target kernel version
- `cache_key`: Cache key for the build
- `status`: Current build status

## Development

### Building

```bash
go build -o drbd-build-server ./cmd/main.go
```

### Running Locally

```bash
./drbd-build-server \
  --addr=:2021 \
  --drbd-version=9.2.12 \
  --cache-dir=./cache \
  --log-level=debug
```

## License

See the main project LICENSE file.





