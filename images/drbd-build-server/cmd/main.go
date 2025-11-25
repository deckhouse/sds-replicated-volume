/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

var GitCommit string

var (
	flagAddr         = flag.String("addr", ":2021", "Server address")
	flagDRBDDir      = flag.String("drbd-dir", "/drbd", "Path to DRBD source directory")
	flagDRBDVersion  = flag.String("drbd-version", "", "DRBD version to clone (e.g., 9.2.12). If empty, will try to use existing drbd-dir")
	flagDRBDRepo     = flag.String("drbd-repo", "https://github.com/LINBIT/drbd.git", "DRBD repository URL")
	flagCacheDir     = flag.String("cache-dir", "/var/cache/drbd-build-server", "Path to cache directory for built modules")
	flagMaxBytesBody = flag.Int64("maxbytesbody", 100*1024*1024, "Maximum number of bytes in the body (100MB)")
	flagKeepTmpDir   = flag.Bool("keeptmpdir", false, "Do not delete the temporary directory, useful for debugging")
	flagCertFile     = flag.String("certfile", "", "Path to a TLS cert file")
	flagKeyFile      = flag.String("keyfile", "", "Path to a TLS key file")
	flagVersion      = flag.Bool("version", false, "Print version and exit")
	flagLogLevel     = flag.String("log-level", "info", "Log level: debug, info, warn, error")
)

type BuildStatus string

const (
	StatusPending   BuildStatus = "pending"
	StatusBuilding  BuildStatus = "building"
	StatusCompleted BuildStatus = "completed"
	StatusFailed    BuildStatus = "failed"
)

type BuildJob struct {
	Key           string
	KernelVersion string
	Status        BuildStatus
	Error         string
	CreatedAt     time.Time
	CompletedAt   *time.Time
	CachePath     string
	mu            sync.RWMutex
}

type BuildResponse struct {
	Status      BuildStatus `json:"status"`
	JobID       string      `json:"job_id,omitempty"`
	Error       string      `json:"error,omitempty"`
	DownloadURL string      `json:"download_url,omitempty"`
}

type server struct {
	router       *mux.Router
	drbdDir      string // Base DRBD directory (if pre-existing)
	cacheDir     string
	maxBytesBody int64
	keepTmpDir   bool
	spaasURL     string
	logger       *slog.Logger // Structured logger

	// Jobs management
	jobs map[string]*BuildJob
	jmu  sync.RWMutex

	// DRBD source configuration
	drbdVersion string // DRBD version to clone (if needed)
	drbdRepo    string // DRBD repository URL
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Printf("Git-commit: '%s'\n", GitCommit)
		os.Exit(0)
	}

	spaasURL := os.Getenv("SPAAS_URL")
	if spaasURL == "" {
		spaasURL = "https://spaas.drbd.io"
	}

	// Get DRBD version from environment or flag
	drbdVersion := *flagDRBDVersion
	if drbdVersion == "" {
		drbdVersion = os.Getenv("DRBD_VERSION")
	}

	// Get DRBD repo from environment or flag
	drbdRepo := *flagDRBDRepo
	if envRepo := os.Getenv("DRBD_REPO"); envRepo != "" {
		drbdRepo = envRepo
	}

	// Initialize logger
	logger := initLogger(*flagLogLevel)

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(*flagCacheDir, 0755); err != nil {
		logger.Error("Failed to create cache directory", "error", err)
		os.Exit(1)
	}

	// Check if base DRBD directory exists
	hasDRBD := false
	if info, err := os.Stat(*flagDRBDDir); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(*flagDRBDDir, "Makefile")); err == nil {
			hasDRBD = true
			logger.Info("DRBD source found", "path", *flagDRBDDir, "note", "will be copied per build")
		}
	}

	// If no base DRBD directory and version specified, we'll clone per build
	if !hasDRBD && drbdVersion == "" {
		logger.Warn("No DRBD source found and no version specified. Builds will fail.")
		logger.Info("Either provide -drbd-dir with existing source or -drbd-version to clone.")
		return
	}

	s := &server{
		router:       mux.NewRouter(),
		drbdDir:      *flagDRBDDir, // Base directory (if exists, will be copied)
		cacheDir:     *flagCacheDir,
		maxBytesBody: *flagMaxBytesBody,
		keepTmpDir:   *flagKeepTmpDir,
		spaasURL:     spaasURL,
		logger:       logger,
		jobs:         make(map[string]*BuildJob),
		drbdVersion:  drbdVersion,
		drbdRepo:     drbdRepo,
	}

	s.routes()

	server := http.Server{
		Addr:           *flagAddr,
		Handler:        s,
		MaxHeaderBytes: 4 * 1024,
		ReadTimeout:    30 * time.Minute,
		WriteTimeout:   30 * time.Minute,
	}

	if *flagCertFile != "" && *flagKeyFile != "" {
		logger.Info("Starting TLS server", "addr", *flagAddr)
		logger.Error("Server failed", "error", server.ListenAndServeTLS(*flagCertFile, *flagKeyFile))
	} else {
		logger.Info("Starting HTTP server", "addr", *flagAddr, "note", "TLS not configured")
		logger.Error("Server failed", "error", server.ListenAndServe())
	}
}

// initLogger initializes structured logger with the specified log level
func initLogger(levelStr string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}

// handler interface, wrapped for MaxBytesReader
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBytesBody)
	s.router.ServeHTTP(w, r)
}

func (s *server) routes() {
	s.router.HandleFunc("/api/v1/build", s.buildModuleHandler()).Methods("POST")
	s.router.HandleFunc("/api/v1/status/{job_id}", s.getStatus()).Methods("GET")
	s.router.HandleFunc("/api/v1/download/{job_id}", s.downloadModule()).Methods("GET")
	s.router.HandleFunc("/api/v1/hello", s.hello()).Methods("GET")
}

func (s *server) hello() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := fmt.Fprintf(w, "Successfully connected to DRBD Build Server ('%s')\n", GitCommit); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

// generateCacheKey creates a unique key for caching based on kernel version and headers hash
func generateCacheKey(kernelVersion string, headersData []byte) string {
	h := sha256.New()
	h.Write([]byte(kernelVersion))
	h.Write(headersData)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *server) buildModuleHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		s.logger.Debug("Received build request", "remote_addr", remoteAddr, "method", r.Method, "path", r.URL.Path)

		// Get kernel version from query parameter or header
		kernelVersion := r.URL.Query().Get("kernel_version")
		if kernelVersion == "" {
			kernelVersion = r.Header.Get("X-Kernel-Version")
			s.logger.Debug("Got kernel version from header", "remote_addr", remoteAddr, "kernel_version", kernelVersion)
		} else {
			s.logger.Debug("Got kernel version from query", "remote_addr", remoteAddr, "kernel_version", kernelVersion)
		}
		if kernelVersion == "" {
			s.logger.Error("kernel_version not provided", "remote_addr", remoteAddr)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "kernel_version parameter or X-Kernel-Version header is required")
			return
		}

		// Validate kernel version format
		// Kernel version format: X.Y.Z[-flavor] or X.Y.Z[-flavor]-build
		// Examples: 5.15.0, 5.15.0-generic, 5.15.0-86-generic
		kernelVersionRegex := regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9_-]+)?(-[0-9]+)?$`)
		if !kernelVersionRegex.MatchString(kernelVersion) {
			s.logger.Error("Invalid kernel version format", "remote_addr", remoteAddr, "kernel_version", kernelVersion)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "Invalid kernel version format. Expected format: X.Y.Z[-flavor] or X.Y.Z[-flavor]-build")
			return
		}

		// Read headers data for cache key generation
		body := r.Body
		defer body.Close()

		s.logger.Debug("Reading kernel headers from request body", "remote_addr", remoteAddr)
		headersData, err := io.ReadAll(body)
		if err != nil {
			s.logger.Error("Failed to read request body", "remote_addr", remoteAddr, "error", err)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "Failed to read request body: %v", err)
			return
		}
		s.logger.Debug("Read kernel headers data", "remote_addr", remoteAddr, "bytes", len(headersData))

		// Generate cache key
		cacheKey := generateCacheKey(kernelVersion, headersData)
		s.logger.Debug("Generated cache key", "remote_addr", remoteAddr, "cache_key", cacheKey)

		// Check if already in cache
		cachePath := filepath.Join(s.cacheDir, cacheKey+".tar.gz")
		s.logger.Debug("Checking cache", "remote_addr", remoteAddr, "cache_path", cachePath)
		info, err := os.Stat(cachePath)
		if err == nil && info.Size() > 0 {
			s.logger.Info("Cache HIT", "remote_addr", remoteAddr, "kernel_version", kernelVersion, "cache_key", cacheKey[:16], "size_bytes", info.Size())
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", kernelVersion))
			s.logger.Debug("Serving cached module file", "remote_addr", remoteAddr)
			http.ServeFile(w, r, cachePath)
			s.logger.Debug("Successfully sent cached module to client", "remote_addr", remoteAddr)
			return
		}
		if err != nil {
			s.logger.Debug("Cache miss", "remote_addr", remoteAddr, "reason", "file not found or error", "error", err)
		} else if info != nil && info.Size() == 0 {
			s.logger.Debug("Cache miss", "remote_addr", remoteAddr, "reason", "file exists but size is 0")
		}

		// Check if build is in progress
		s.logger.Debug("Checking for existing job", "remote_addr", remoteAddr, "cache_key", cacheKey[:16])
		s.jmu.RLock()
		job, exists := s.jobs[cacheKey]
		activeJobsCount := len(s.jobs)
		s.jmu.RUnlock()
		s.logger.Debug("Total active jobs", "remote_addr", remoteAddr, "count", activeJobsCount)

		if exists {
			job.mu.RLock()
			status := job.Status
			jobID := job.Key
			createdAt := job.CreatedAt
			job.mu.RUnlock()
			s.logger.Debug("Found existing job", "remote_addr", remoteAddr, "job_id", jobID[:16], "status", status, "created_at", createdAt.Format(time.RFC3339))

			if status == StatusBuilding || status == StatusPending {
				s.logger.Info("Build already in progress", "remote_addr", remoteAddr, "kernel_version", kernelVersion, "job_id", jobID[:16], "status", status)
				resp := BuildResponse{
					Status: status,
					JobID:  jobID,
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				s.logger.Debug("Returning job ID to client", "remote_addr", remoteAddr, "job_id", jobID)
				if err := json.NewEncoder(w).Encode(resp); err != nil {
					s.logger.Error("Failed to encode response", "remote_addr", remoteAddr, "error", err)
				}
				return
			}

			if status == StatusCompleted {
				job.mu.RLock()
				cachePath := job.CachePath
				completedAt := job.CompletedAt
				job.mu.RUnlock()
				s.logger.Debug("Job completed, checking cache file", "remote_addr", remoteAddr, "cache_path", cachePath)
				if cachePath != "" {
					info, err := os.Stat(cachePath)
					if err == nil {
						s.logger.Info("Serving completed build", "remote_addr", remoteAddr, "kernel_version", kernelVersion, "completed_at", completedAt, "size_bytes", info.Size())
						w.Header().Set("Content-Type", "application/gzip")
						w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", kernelVersion))
						http.ServeFile(w, r, cachePath)
						s.logger.Debug("Successfully sent completed build to client", "remote_addr", remoteAddr)
						return
					}
					s.logger.Warn("Cache file for completed job not found", "remote_addr", remoteAddr, "error", err)
				}
			}

			if status == StatusFailed {
				job.mu.RLock()
				errorMsg := job.Error
				job.mu.RUnlock()
				s.logger.Debug("Previous job failed, creating new one", "remote_addr", remoteAddr, "error", errorMsg)
			}
		} else {
			s.logger.Debug("No existing job found, will create new one", "remote_addr", remoteAddr)
		}

		// Create new job
		s.logger.Debug("Creating new build job", "remote_addr", remoteAddr)
		job = &BuildJob{
			Key:           cacheKey,
			KernelVersion: kernelVersion,
			Status:        StatusPending,
			CreatedAt:     time.Now(),
			CachePath:     cachePath,
		}

		s.jmu.Lock()
		s.jobs[cacheKey] = job
		activeJobsCount = len(s.jobs)
		s.jmu.Unlock()
		s.logger.Info("Created new build job", "remote_addr", remoteAddr, "job_id", cacheKey[:16], "kernel_version", kernelVersion, "cache_path", cachePath, "total_jobs", activeJobsCount)

		s.logger.Info("Starting DRBD build", "remote_addr", remoteAddr, "kernel_version", kernelVersion, "job_id", cacheKey[:16])

		// Start build in background
		s.logger.Debug("Launching async build goroutine", "remote_addr", remoteAddr, "job_id", cacheKey[:16])
		go s.buildModule(job, headersData)

		// Return job ID immediately
		resp := BuildResponse{
			Status: StatusBuilding,
			JobID:  cacheKey,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		s.logger.Debug("Returning job ID to client with status 202 Accepted", "remote_addr", remoteAddr, "job_id", cacheKey[:16])
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			s.logger.Error("Failed to encode response", "remote_addr", remoteAddr, "error", err)
		}
	}
}

func (s *server) buildModule(job *BuildJob, headersData []byte) {
	jobID := job.Key[:16]
	s.logger.Debug("Async build started", "job_id", jobID, "kernel_version", job.KernelVersion)

	job.mu.Lock()
	job.Status = StatusBuilding
	startTime := time.Now()
	job.mu.Unlock()
	s.logger.Debug("Job status set to BUILDING", "job_id", jobID)

	defer func() {
		job.mu.Lock()
		now := time.Now()
		job.CompletedAt = &now
		duration := now.Sub(startTime)
		status := job.Status
		job.mu.Unlock()
		s.logger.Debug("Build completed", "job_id", jobID, "status", status, "duration", duration)
	}()

	// Create temporary directory for kernel headers
	s.logger.Debug("Creating temporary directory", "job_id", jobID)
	tmpDir, err := os.MkdirTemp("", "drbd-build-server-*")
	if err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create temporary directory: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}
	s.logger.Debug("Created temporary directory", "job_id", jobID, "tmp_dir", tmpDir)

	if !s.keepTmpDir {
		defer func() {
			if err := os.RemoveAll(tmpDir); err != nil {
				s.logger.Warn("Failed to remove temporary directory", "job_id", jobID, "tmp_dir", tmpDir, "error", err)
			}
		}()
	} else {
		s.logger.Info("Keeping temporary directory", "job_id", jobID, "tmp_dir", tmpDir)
	}

	// Extract kernel headers from request body
	kernelHeadersDir := filepath.Join(tmpDir, "kernel-headers")
	s.logger.Debug("Creating kernel headers directory", "job_id", jobID, "kernel_headers_dir", kernelHeadersDir)
	if err := os.MkdirAll(kernelHeadersDir, 0755); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create kernel headers directory: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	// Extract tar.gz archive
	s.logger.Debug("Extracting kernel headers archive", "job_id", jobID, "bytes", len(headersData))
	headersReader := bytes.NewReader(headersData)
	if err := extractTarGz(headersReader, kernelHeadersDir); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to extract kernel headers: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}
	s.logger.Debug("Successfully extracted kernel headers", "job_id", jobID, "kernel_headers_dir", kernelHeadersDir)

	// Find the kernel build directory
	buildDir, err := s.findKernelBuildDir(kernelHeadersDir, job.KernelVersion, jobID)
	if err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = err.Error()
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}
	s.logger.Info("Using kernel build directory", "job_id", jobID, "build_dir", buildDir)

	// Prepare DRBD source for this build (create isolated copy)
	s.logger.Info("Preparing DRBD source (isolated copy for this build)", "job_id", jobID)
	drbdBuildDir := filepath.Join(tmpDir, "drbd")
	if err := s.prepareDRBDForBuild(drbdBuildDir, jobID); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to prepare DRBD source: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}
	s.logger.Debug("DRBD source prepared", "job_id", jobID, "drbd_build_dir", drbdBuildDir)

	// Cleanup DRBD copy after build (unless keepTmpDir is set)
	if !s.keepTmpDir {
		defer func() {
			s.logger.Debug("Cleaning up DRBD build directory", "job_id", jobID, "drbd_build_dir", drbdBuildDir)
			if err := os.RemoveAll(drbdBuildDir); err != nil {
				s.logger.Warn("Failed to remove DRBD build directory", "job_id", jobID, "error", err)
			}
		}()
	}

	// Build DRBD module
	modulesDir := filepath.Join(tmpDir, "modules")
	s.logger.Debug("Creating modules output directory", "job_id", jobID, "modules_dir", modulesDir)
	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create modules directory: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	s.logger.Info("Starting DRBD module build process", "job_id", jobID)
	if err := s.buildDRBD(job.KernelVersion, buildDir, modulesDir, drbdBuildDir, jobID); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = err.Error()
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", err.Error())
		return
	}
	s.logger.Debug("DRBD module build completed successfully", "job_id", jobID)

	// Collect .ko files
	s.logger.Debug("Searching for .ko files", "job_id", jobID, "modules_dir", modulesDir)
	koFiles, err := findKOFiles(modulesDir)
	if err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to find .ko files: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	s.logger.Debug("Found .ko files", "job_id", jobID, "count", len(koFiles))
	if len(koFiles) == 0 {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("No .ko files found after build in %s", modulesDir)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	for i, koFile := range koFiles {
		s.logger.Debug("Found .ko file", "job_id", jobID, "index", i+1, "file", koFile)
	}

	// Create tar.gz archive with .ko files and save to cache
	cachePath := job.CachePath
	s.logger.Debug("Creating cache file", "job_id", jobID, "cache_path", cachePath)
	cacheFile, err := os.Create(cachePath)
	if err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create cache file: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}
	defer cacheFile.Close()

	s.logger.Debug("Creating tar.gz archive", "job_id", jobID, "ko_files_count", len(koFiles))
	if err := createTarGz(cacheFile, koFiles, modulesDir); err != nil {
		os.Remove(cachePath)
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create tar.gz: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	cacheFile.Close()
	cacheInfo, _ := os.Stat(cachePath)
	if cacheInfo != nil {
		s.logger.Debug("Cache file created successfully", "job_id", jobID, "size_bytes", cacheInfo.Size())
	}

	// Update job status
	job.mu.Lock()
	job.Status = StatusCompleted
	job.mu.Unlock()
	s.logger.Info("Successfully built DRBD modules", "job_id", jobID, "kernel_version", job.KernelVersion, "cache_path", cachePath, "size_bytes", cacheInfo.Size())
}

func (s *server) getStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		vars := mux.Vars(r)
		jobID := vars["job_id"]
		s.logger.Debug("Status request for job", "remote_addr", remoteAddr, "job_id", jobID)

		s.jmu.RLock()
		job, exists := s.jobs[jobID]
		s.jmu.RUnlock()

		if !exists {
			s.logger.Debug("Job not found", "remote_addr", remoteAddr, "job_id", jobID)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Job not found: %s", jobID)
			return
		}

		job.mu.RLock()
		status := job.Status
		errorMsg := job.Error
		kernelVersion := job.KernelVersion
		createdAt := job.CreatedAt
		completedAt := job.CompletedAt
		cachePath := job.CachePath
		job.mu.RUnlock()

		duration := ""
		if completedAt != nil {
			duration = completedAt.Sub(createdAt).String()
		} else {
			duration = time.Since(createdAt).String()
		}
		s.logger.Debug("Job status", "remote_addr", remoteAddr, "status", status, "kernel_version", kernelVersion, "duration", duration, "has_error", errorMsg != "")

		resp := BuildResponse{
			Status: status,
			Error:  errorMsg,
		}

		if status == StatusCompleted {
			resp.DownloadURL = fmt.Sprintf("/api/v1/download/%s", jobID)
			s.logger.Debug("Job completed", "remote_addr", remoteAddr, "download_url", resp.DownloadURL)
		}

		w.Header().Set("Content-Type", "application/json")
		if status == StatusCompleted || status == StatusFailed {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusAccepted)
		}

		// Add extra info for debugging
		respData := map[string]interface{}{
			"status":         status,
			"job_id":         jobID,
			"kernel_version": kernelVersion,
			"created_at":     createdAt.Format(time.RFC3339),
			"error":          errorMsg,
			"cache_path":     cachePath,
		}
		if completedAt != nil {
			respData["completed_at"] = completedAt.Format(time.RFC3339)
			respData["duration"] = duration
		}
		if status == StatusCompleted {
			respData["download_url"] = fmt.Sprintf("/api/v1/download/%s", jobID)
		}

		if err := json.NewEncoder(w).Encode(respData); err != nil {
			s.logger.Error("Failed to encode response", "remote_addr", remoteAddr, "error", err)
		}
	}
}

func (s *server) downloadModule() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		vars := mux.Vars(r)
		jobID := vars["job_id"]
		s.logger.Debug("Download request for job", "remote_addr", remoteAddr, "job_id", jobID)

		s.jmu.RLock()
		job, exists := s.jobs[jobID]
		s.jmu.RUnlock()

		if !exists {
			s.logger.Debug("Job not found", "remote_addr", remoteAddr, "job_id", jobID)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Job not found: %s", jobID)
			return
		}

		job.mu.RLock()
		status := job.Status
		cachePath := job.CachePath
		kernelVersion := job.KernelVersion
		job.mu.RUnlock()

		s.logger.Debug("Job status", "remote_addr", remoteAddr, "status", status, "cache_path", cachePath)

		if status != StatusCompleted {
			s.logger.Debug("Job not completed", "remote_addr", remoteAddr, "status", status)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "Job is not completed yet. Status: %s", status)
			return
		}

		if cachePath == "" {
			s.logger.Debug("Cache path not set for completed job", "remote_addr", remoteAddr)
			s.errorf(http.StatusInternalServerError, remoteAddr, w, "Cache path not set for completed job")
			return
		}

		cacheInfo, err := os.Stat(cachePath)
		if os.IsNotExist(err) {
			s.logger.Debug("Cache file not found", "remote_addr", remoteAddr, "cache_path", cachePath)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Cache file not found: %s", cachePath)
			return
		}

		s.logger.Info("Serving module file", "remote_addr", remoteAddr, "cache_path", cachePath, "size_bytes", cacheInfo.Size())
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", kernelVersion))
		http.ServeFile(w, r, cachePath)
		s.logger.Debug("Module file sent successfully", "remote_addr", remoteAddr)
	}
}

func (s *server) buildDRBD(kernelVersion, kernelBuildDir, outputDir, drbdDir, jobID string) error {
	s.logger.Debug("Starting buildDRBD", "job_id", jobID, "kernel_version", kernelVersion, "kdir", kernelBuildDir, "output", outputDir, "drbd_dir", drbdDir)

	// Change to DRBD directory
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			s.logger.Warn("Failed to restore original directory", "job_id", jobID, "error", err)
		}
	}()

	s.logger.Debug("Changing to DRBD directory", "job_id", jobID, "drbd_dir", drbdDir)
	if err := os.Chdir(drbdDir); err != nil {
		return fmt.Errorf("failed to change to DRBD directory %s: %v", drbdDir, err)
	}

	// Verify DRBD Makefile exists
	s.logger.Debug("Verifying DRBD Makefile exists", "job_id", jobID)
	if _, err := os.Stat("Makefile"); os.IsNotExist(err) {
		return fmt.Errorf("DRBD Makefile not found in %s", drbdDir)
	}
	s.logger.Debug("DRBD Makefile found", "job_id", jobID)

	// Set environment variables for make
	// Use system environment variables as base and add DRBD-specific ones
	// This ensures PATH and other important variables are available
	env := os.Environ()
	env = append(
		env,
		fmt.Sprintf("KVER=%s", kernelVersion),
		fmt.Sprintf("KDIR=%s", kernelBuildDir),
		fmt.Sprintf("SPAAS_URL=%s", s.spaasURL),
		"SPAAS=true",
	)
	s.logger.Debug("Environment variables set", "job_id", jobID, "KVER", kernelVersion, "KDIR", kernelBuildDir, "SPAAS_URL", s.spaasURL)

	// Clean previous build
	s.logger.Debug("Running 'make clean'", "job_id", jobID)
	cmd := exec.Command("make", "clean")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = drbdDir
	if err := cmd.Run(); err != nil {
		// Don't fail on clean errors
		s.logger.Debug("make clean failed (ignored)", "job_id", jobID, "error", err)
	} else {
		s.logger.Debug("'make clean' completed successfully", "job_id", jobID)
	}

	// Check and update submodules if needed (required by make module)
	// This ensures all submodules are initialized before building
	s.logger.Debug("Checking submodules", "job_id", jobID)
	cmd = exec.Command("make", "check-submods")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = drbdDir
	if err := cmd.Run(); err != nil {
		s.logger.Debug("make check-submods failed (may be OK if no submodules)", "job_id", jobID, "error", err)
	} else {
		s.logger.Debug("Submodules check completed", "job_id", jobID)
	}

	// Build module
	s.logger.Info("Running 'make module' (this may take several minutes)", "job_id", jobID)
	startTime := time.Now()
	cmd = exec.Command("make", "module")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = drbdDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make module failed: %v", err)
	}
	buildDuration := time.Since(startTime)
	s.logger.Debug("'make module' completed successfully", "job_id", jobID, "duration", buildDuration)

	// Install modules to output directory
	s.logger.Debug("Installing modules to output directory", "job_id", jobID, "output_dir", outputDir)
	cmd = exec.Command("make", "install")
	env = append(env, fmt.Sprintf("DESTDIR=%s", outputDir))
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = drbdDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make install failed: %v", err)
	}
	s.logger.Debug("Modules installed successfully", "job_id", jobID, "output_dir", outputDir)

	return nil
}

// findKernelBuildDir searches for the kernel build directory in the extracted headers.
// It tries multiple locations in order:
// 1. Standard location: /lib/modules/KVER/build
// 2. Alternative location: /usr/src/linux-headers-KVER
// 3. Search for any Makefile in the extracted headers
// Returns the build directory path or an error if not found.
func (s *server) findKernelBuildDir(kernelHeadersDir, kernelVersion, jobID string) (string, error) {
	s.logger.Debug("Searching for kernel build directory", "job_id", jobID)

	// Step 1: Try standard location (/lib/modules/KVER/build)
	buildDir := filepath.Join(kernelHeadersDir, "lib", "modules", kernelVersion, "build")
	s.logger.Debug("Trying standard location", "job_id", jobID, "build_dir", buildDir)
	if info, err := os.Stat(buildDir); err == nil && info.IsDir() {
		makefilePath := filepath.Join(buildDir, "Makefile")
		if _, err := os.Stat(makefilePath); err == nil {
			s.logger.Debug("Found build directory in standard location", "job_id", jobID)
			return buildDir, nil
		}
		s.logger.Debug("Standard location exists but Makefile not found", "job_id", jobID)
	}

	// Step 2: Try alternative location (/usr/src/linux-headers-KVER)
	buildDir = filepath.Join(kernelHeadersDir, "usr", "src", "linux-headers-"+kernelVersion)
	s.logger.Debug("Trying alternative location", "job_id", jobID, "build_dir", buildDir)
	if info, err := os.Stat(buildDir); err == nil && info.IsDir() {
		makefilePath := filepath.Join(buildDir, "Makefile")
		if _, err := os.Stat(makefilePath); err == nil {
			s.logger.Debug("Found build directory in alternative location", "job_id", jobID)
			return buildDir, nil
		}
		s.logger.Debug("Alternative location exists but Makefile not found", "job_id", jobID)
	}

	// Step 3: Search for any Makefile in the extracted headers
	s.logger.Debug("Standard locations not found, searching for Makefile", "job_id", jobID)
	matches, err := filepath.Glob(filepath.Join(kernelHeadersDir, "**", "Makefile"))
	if err != nil {
		return "", fmt.Errorf("failed to search for Makefile: %v", err)
	}
	s.logger.Debug("Found Makefile(s) in extracted headers", "job_id", jobID, "count", len(matches))

	if len(matches) > 0 {
		// Use the first Makefile found (usually the kernel build Makefile)
		buildDir = filepath.Dir(matches[0])
		s.logger.Debug("Using build directory from found Makefile", "job_id", jobID, "build_dir", buildDir)
		// Verify Makefile exists
		makefilePath := filepath.Join(buildDir, "Makefile")
		if _, err := os.Stat(makefilePath); err == nil {
			return buildDir, nil
		}
	}

	// Step 4: Validation failed - return error
	return "", fmt.Errorf("kernel build directory not found for version %s. Searched in: %s/lib/modules/%s/build, %s/usr/src/linux-headers-%s, and all Makefiles in archive",
		kernelVersion, kernelHeadersDir, kernelVersion, kernelHeadersDir, kernelVersion)
}

// prepareDRBDForBuild prepares an isolated DRBD source directory for a build.
// It either copies from base drbdDir or clones from repository if version is specified.
func (s *server) prepareDRBDForBuild(destDir, jobID string) error {
	s.logger.Debug("Preparing DRBD source", "job_id", jobID, "dest_dir", destDir)

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create DRBD build directory: %v", err)
	}

	// Check if base DRBD directory exists
	if s.drbdDir != "" {
		if info, err := os.Stat(s.drbdDir); err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(s.drbdDir, "Makefile")); err == nil {
				// Copy existing DRBD directory
				s.logger.Info("Copying DRBD source", "job_id", jobID, "src", s.drbdDir, "dst", destDir)
				if err := copyDirectory(s.drbdDir, destDir, jobID, s.logger); err != nil {
					return fmt.Errorf("failed to copy DRBD directory: %v", err)
				}
				s.logger.Debug("DRBD source copied successfully", "job_id", jobID)
				return nil
			}
		}
	}

	// If no base directory or version specified, clone from repository
	if s.drbdVersion == "" {
		return fmt.Errorf("no DRBD source available: neither drbd-dir exists nor drbd-version specified")
	}

	s.logger.Info("Cloning DRBD repository", "job_id", jobID, "version", s.drbdVersion)
	if err := s.cloneDRBDRepoToDir(s.drbdVersion, s.drbdRepo, destDir, jobID); err != nil {
		return fmt.Errorf("failed to clone DRBD repository: %v", err)
	}
	s.logger.Debug("DRBD repository cloned successfully", "job_id", jobID)
	return nil
}

// copyDirectory recursively copies a directory from src to dst.
// Uses cp -a to preserve permissions, timestamps, and handle symlinks.
func copyDirectory(src, dst, jobID string, logger *slog.Logger) error {
	logger.Debug("Copying directory", "job_id", jobID, "src", src, "dst", dst)

	// Use cp -a to copy recursively with all attributes preserved
	// -a = archive mode (equivalent to -dR --preserve=all)
	cmd := exec.Command("cp", "-a", src+"/.", dst)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to copy directory: %v, output: %s", err, string(output))
	}

	logger.Debug("Directory copied successfully", "job_id", jobID)
	return nil
}

// cloneDRBDRepoToDir clones the DRBD repository to the specified directory
func (s *server) cloneDRBDRepoToDir(version, repoURL, destDir, jobID string) error {
	s.logger.Debug("Cloning DRBD repository", "job_id", jobID, "version", version, "repo", repoURL, "dest", destDir)

	// Determine branch name (format: drbd-9.2.12)
	branch := fmt.Sprintf("drbd-%s", version)
	s.logger.Debug("Cloning branch", "job_id", jobID, "branch", branch)

	// Clone repository directly to destination
	s.logger.Debug("Running git clone", "job_id", jobID, "branch", branch, "repo", repoURL, "dest", destDir)
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", branch, repoURL, destDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %v", err)
	}

	// Get git hash before removing .git directory
	gitHash := "unknown"
	hashCmd := exec.Command("git", "rev-parse", "HEAD")
	hashCmd.Dir = destDir
	if output, err := hashCmd.Output(); err == nil {
		gitHash = strings.TrimSpace(string(output))
		s.logger.Debug("Got git hash", "job_id", jobID, "git_hash", gitHash)
	} else {
		s.logger.Warn("Failed to get git hash", "job_id", jobID, "error", err)
	}

	// Update submodules
	s.logger.Debug("Updating submodules", "job_id", jobID)
	cmd = exec.Command("git", "submodule", "update", "--init", "--recursive")
	cmd.Dir = destDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git submodule update failed: %v", err)
	}

	// Remove .git directory
	s.logger.Debug("Removing .git directory", "job_id", jobID)
	if err := os.RemoveAll(filepath.Join(destDir, ".git")); err != nil {
		s.logger.Warn("Failed to remove .git directory", "job_id", jobID, "error", err)
	}

	// Create drbd/.drbd_git_revision file
	gitRevisionPath := filepath.Join(destDir, "drbd", ".drbd_git_revision")
	gitRevisionDir := filepath.Dir(gitRevisionPath)
	if err := os.MkdirAll(gitRevisionDir, 0755); err != nil {
		return fmt.Errorf("failed to create drbd directory: %v", err)
	}

	gitRevisionContent := fmt.Sprintf("GIT-hash:%s\n", gitHash)
	if err := os.WriteFile(gitRevisionPath, []byte(gitRevisionContent), 0644); err != nil {
		return fmt.Errorf("failed to create .drbd_git_revision file: %v", err)
	}
	s.logger.Debug("Created drbd/.drbd_git_revision", "job_id", jobID, "git_hash", gitHash)

	return nil
}

func extractTarGz(r io.Reader, destDir string) error {
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %v", err)
		}

		targetPath := filepath.Join(destDir, header.Name)

		// Security: prevent directory traversal
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destDir)+string(os.PathSeparator)) &&
			filepath.Clean(targetPath) != filepath.Clean(destDir) {
			return fmt.Errorf("invalid path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory %s: %v", targetPath, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory: %v", err)
			}
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file %s: %v", targetPath, err)
			}
			if _, err := io.Copy(file, tarReader); err != nil {
				file.Close()
				return fmt.Errorf("failed to write file %s: %v", targetPath, err)
			}
			file.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				return fmt.Errorf("failed to create symlink %s -> %s: %v", targetPath, header.Linkname, err)
			}
		}
	}

	return nil
}

func findKOFiles(rootDir string) ([]string, error) {
	var koFiles []string

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".ko") {
			relPath, err := filepath.Rel(rootDir, path)
			if err != nil {
				return err
			}
			koFiles = append(koFiles, relPath)
		}
		return nil
	})

	return koFiles, err
}

func createTarGz(w io.Writer, files []string, baseDir string) error {
	gzWriter := gzip.NewWriter(w)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	for _, file := range files {
		fullPath := filepath.Join(baseDir, file)
		info, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("failed to stat file %s: %v", fullPath, err)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("failed to create tar header for %s: %v", file, err)
		}

		// Use forward slashes and preserve relative path structure
		header.Name = file
		header.Format = tar.FormatGNU

		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header for %s: %v", file, err)
		}

		fileReader, err := os.Open(fullPath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %v", fullPath, err)
		}

		if _, err := io.Copy(tarWriter, fileReader); err != nil {
			fileReader.Close()
			return fmt.Errorf("failed to write file %s to tar: %v", file, err)
		}

		fileReader.Close()
	}

	return nil
}

func (s *server) errorf(code int, remoteAddr string, w http.ResponseWriter, format string, a ...interface{}) {
	w.WriteHeader(code)
	msg := fmt.Sprintf(format, a...)
	_, _ = fmt.Fprint(w, msg)
	s.logger.Error("HTTP error", "remote_addr", remoteAddr, "code", code, "error", msg)
}
