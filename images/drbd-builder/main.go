package main

// TODO: Add ctx where possible
// TODO: Test with distroless
// TODO: check if repo copies not move
// TODO: rename to drbd-build-server

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
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	flagCacheDir     = flag.String("cache-dir", "/var/cache/drbd-builder", "Path to cache directory for built modules")
	flagMaxBytesBody = flag.Int64("maxbytesbody", 100*1024*1024, "Maximum number of bytes in the body (100MB)")
	flagKeepTmpDir   = flag.Bool("keeptmpdir", false, "Do not delete the temporary directory, useful for debugging")
	flagCertFile     = flag.String("certfile", "", "Path to a TLS cert file")
	flagKeyFile      = flag.String("keyfile", "", "Path to a TLS key file")
	flagVersion      = flag.Bool("version", false, "Print version and exit")
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

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(*flagCacheDir, 0755); err != nil {
		log.Fatalf("Failed to create cache directory: %v", err)
	}

	// Check if base DRBD directory exists
	hasDRBD := false
	if info, err := os.Stat(*flagDRBDDir); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(*flagDRBDDir, "Makefile")); err == nil {
			hasDRBD = true
			log.Printf("DRBD source found at %s (will be copied per build)", *flagDRBDDir)
		}
	}

	// If no base DRBD directory and version specified, we'll clone per build
	if !hasDRBD && drbdVersion == "" {
		log.Printf("WARNING: No DRBD source found and no version specified. Builds will fail.")
		log.Printf("Either provide -drbd-dir with existing source or -drbd-version to clone.")
		return
	}

	s := &server{
		router:       mux.NewRouter(),
		drbdDir:      *flagDRBDDir, // Base directory (if exists, will be copied)
		cacheDir:     *flagCacheDir,
		maxBytesBody: *flagMaxBytesBody,
		keepTmpDir:   *flagKeepTmpDir,
		spaasURL:     spaasURL,
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
		log.Printf("Starting TLS server on %s", *flagAddr)
		log.Fatal(server.ListenAndServeTLS(*flagCertFile, *flagKeyFile))
	} else {
		log.Printf("Starting HTTP server on %s (TLS not configured)", *flagAddr)
		log.Fatal(server.ListenAndServe())
	}
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
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := fmt.Fprintf(w, "Successfully connected to DRBD Builder ('%s')\n", GitCommit); err != nil {
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
		log.Printf("[DEBUG] [%s] Received build request: method=%s, path=%s", remoteAddr, r.Method, r.URL.Path)

		// Get kernel version from query parameter or header
		kernelVersion := r.URL.Query().Get("kernel_version")
		if kernelVersion == "" {
			kernelVersion = r.Header.Get("X-Kernel-Version")
			log.Printf("[DEBUG] [%s] Got kernel version from X-Kernel-Version header: %s", remoteAddr, kernelVersion)
		} else {
			log.Printf("[DEBUG] [%s] Got kernel version from query parameter: %s", remoteAddr, kernelVersion)
		}
		if kernelVersion == "" {
			log.Printf("[DEBUG] [%s] ERROR: kernel_version not provided", remoteAddr)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "kernel_version parameter or X-Kernel-Version header is required")
			return
		}

		// Validate kernel version format
		// TODO: Add regex for validation

		// Read headers data for cache key generation
		body := r.Body
		defer body.Close()

		log.Printf("[DEBUG] [%s] Reading kernel headers from request body...", remoteAddr)
		headersData, err := io.ReadAll(body)
		if err != nil {
			log.Printf("[DEBUG] [%s] ERROR: Failed to read request body: %v", remoteAddr, err)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "Failed to read request body: %v", err)
			return
		}
		log.Printf("[DEBUG] [%s] Read %d bytes of kernel headers data", remoteAddr, len(headersData))

		// Generate cache key
		cacheKey := generateCacheKey(kernelVersion, headersData)
		log.Printf("[DEBUG] [%s] Generated cache key: %s", remoteAddr, cacheKey)

		// Check if already in cache
		cachePath := filepath.Join(s.cacheDir, cacheKey+".tar.gz")
		log.Printf("[DEBUG] [%s] Checking cache at path: %s", remoteAddr, cachePath)
		info, err := os.Stat(cachePath)
		if err == nil && info.Size() > 0 {
			log.Printf("[INFO] [%s] Cache HIT for kernel %s (key: %s, size: %d bytes)", remoteAddr, kernelVersion, cacheKey[:16], info.Size())
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", kernelVersion))
			log.Printf("[DEBUG] [%s] Serving cached module file to client...", remoteAddr)
			http.ServeFile(w, r, cachePath)
			log.Printf("[DEBUG] [%s] Successfully sent cached module to client", remoteAddr)
			return
		}
		if err != nil {
			log.Printf("[DEBUG] [%s] Cache miss (file not found or error): %v", remoteAddr, err)
		} else if info != nil && info.Size() == 0 {
			log.Printf("[DEBUG] [%s] Cache miss (file exists but size is 0)", remoteAddr)
		}

		// Check if build is in progress
		log.Printf("[DEBUG] [%s] Checking for existing job with key: %s", remoteAddr, cacheKey[:16])
		s.jmu.RLock()
		job, exists := s.jobs[cacheKey]
		activeJobsCount := len(s.jobs)
		s.jmu.RUnlock()
		log.Printf("[DEBUG] [%s] Total active jobs: %d", remoteAddr, activeJobsCount)

		if exists {
			job.mu.RLock()
			status := job.Status
			jobID := job.Key
			createdAt := job.CreatedAt
			job.mu.RUnlock()
			log.Printf("[DEBUG] [%s] Found existing job: id=%s, status=%s, created_at=%s", remoteAddr, jobID[:16], status, createdAt.Format(time.RFC3339))

			if status == StatusBuilding || status == StatusPending {
				log.Printf("[INFO] [%s] Build already in progress for kernel %s (job: %s, status: %s)", remoteAddr, kernelVersion, jobID[:16], status)
				resp := BuildResponse{
					Status: status,
					JobID:  jobID,
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				log.Printf("[DEBUG] [%s] Returning job ID to client: %s", remoteAddr, jobID)
				json.NewEncoder(w).Encode(resp)
				return
			}

			if status == StatusCompleted {
				job.mu.RLock()
				cachePath := job.CachePath
				completedAt := job.CompletedAt
				job.mu.RUnlock()
				log.Printf("[DEBUG] [%s] Job completed, checking cache file: %s", remoteAddr, cachePath)
				if cachePath != "" {
					if info, err := os.Stat(cachePath); err == nil {
						log.Printf("[INFO] [%s] Serving completed build for kernel %s (completed_at: %v, size: %d bytes)",
							remoteAddr, kernelVersion, completedAt, info.Size())
						w.Header().Set("Content-Type", "application/gzip")
						w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", kernelVersion))
						http.ServeFile(w, r, cachePath)
						log.Printf("[DEBUG] [%s] Successfully sent completed build to client", remoteAddr)
						return
					} else {
						log.Printf("[DEBUG] [%s] WARNING: Cache file for completed job not found: %v", remoteAddr, err)
					}
				}
			}

			if status == StatusFailed {
				job.mu.RLock()
				errorMsg := job.Error
				job.mu.RUnlock()
				log.Printf("[DEBUG] [%s] Previous job failed, creating new one. Error: %s", remoteAddr, errorMsg)
			}
		} else {
			log.Printf("[DEBUG] [%s] No existing job found, will create new one", remoteAddr)
		}

		// Create new job
		log.Printf("[DEBUG] [%s] Creating new build job...", remoteAddr)
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
		log.Printf("[INFO] [%s] Created new build job: id=%s, kernel=%s, cache_path=%s, total_jobs=%d",
			remoteAddr, cacheKey[:16], kernelVersion, cachePath, activeJobsCount)

		log.Printf("[INFO] [%s] Starting DRBD build for kernel version: %s (job: %s)", remoteAddr, kernelVersion, cacheKey[:16])

		// Start build in background
		log.Printf("[DEBUG] [%s] Launching async build goroutine for job %s", remoteAddr, cacheKey[:16])
		go s.buildModule(job, headersData, remoteAddr)

		// Return job ID immediately
		resp := BuildResponse{
			Status: StatusBuilding,
			JobID:  cacheKey,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		log.Printf("[DEBUG] [%s] Returning job ID %s to client with status 202 Accepted", remoteAddr, cacheKey[:16])
		json.NewEncoder(w).Encode(resp)
	}
}

func (s *server) buildModule(job *BuildJob, headersData []byte, remoteAddr string) {
	jobID := job.Key[:16]
	log.Printf("[DEBUG] [job:%s] Async build started for kernel %s", jobID, job.KernelVersion)

	job.mu.Lock()
	job.Status = StatusBuilding
	startTime := time.Now()
	job.mu.Unlock()
	log.Printf("[DEBUG] [job:%s] Job status set to BUILDING", jobID)

	defer func() {
		job.mu.Lock()
		now := time.Now()
		job.CompletedAt = &now
		duration := now.Sub(startTime)
		status := job.Status
		job.mu.Unlock()
		log.Printf("[DEBUG] [job:%s] Build completed with status=%s, duration=%v", jobID, status, duration)
	}()

	// Create temporary directory for kernel headers
	log.Printf("[DEBUG] [job:%s] Creating temporary directory...", jobID)
	tmpDir, err := os.MkdirTemp("", "drbd-builder-*")
	if err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create temporary directory: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] Build failed: %s", jobID, job.Error)
		return
	}
	log.Printf("[DEBUG] [job:%s] Created temporary directory: %s", jobID, tmpDir)

	if !s.keepTmpDir {
		defer func() {
			if err := os.RemoveAll(tmpDir); err != nil {
				log.Printf("Failed to remove temporary directory %s: %v", tmpDir, err)
			}
		}()
	} else {
		log.Printf("Keeping temporary directory: %s", tmpDir)
	}

	// Extract kernel headers from request body
	kernelHeadersDir := filepath.Join(tmpDir, "kernel-headers")
	log.Printf("[DEBUG] [job:%s] Creating kernel headers directory: %s", jobID, kernelHeadersDir)
	if err := os.MkdirAll(kernelHeadersDir, 0755); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create kernel headers directory: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}

	// Extract tar.gz archive
	log.Printf("[DEBUG] [job:%s] Extracting kernel headers archive (%d bytes)...", jobID, len(headersData))
	headersReader := bytes.NewReader(headersData)
	if err := extractTarGz(headersReader, kernelHeadersDir); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to extract kernel headers: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}
	log.Printf("[DEBUG] [job:%s] Successfully extracted kernel headers to %s", jobID, kernelHeadersDir)

	// Find the build directory (usually /lib/modules/KVER/build)
	// TODO: Move to separate method & write step-by-step comment
	// TODO: Use early return
	// TODO: Check if KVER is valid
	log.Printf("[DEBUG] [job:%s] Searching for kernel build directory...", jobID)
	buildDir := filepath.Join(kernelHeadersDir, "lib", "modules", job.KernelVersion, "build")
	log.Printf("[DEBUG] [job:%s] Trying standard location: %s", jobID, buildDir)
	if _, err := os.Stat(buildDir); os.IsNotExist(err) {
		log.Printf("[DEBUG] [job:%s] Standard location not found, trying alternative...", jobID)
		// Try alternative location
		buildDir = filepath.Join(kernelHeadersDir, "usr", "src", "linux-headers-"+job.KernelVersion)
		log.Printf("[DEBUG] [job:%s] Trying alternative location: %s", jobID, buildDir)
		if _, err := os.Stat(buildDir); os.IsNotExist(err) {
			log.Printf("[DEBUG] [job:%s] Alternative location not found, searching for Makefile...", jobID)
			// Try to find any build directory
			matches, _ := filepath.Glob(filepath.Join(kernelHeadersDir, "**", "Makefile"))
			log.Printf("[DEBUG] [job:%s] Found %d Makefile(s) in extracted headers", jobID, len(matches))
			if len(matches) > 0 {
				buildDir = filepath.Dir(matches[0])
				log.Printf("[DEBUG] [job:%s] Using build directory from found Makefile: %s", jobID, buildDir)
			} else {
				job.mu.Lock()
				job.Status = StatusFailed
				job.Error = fmt.Sprintf("Kernel build directory not found for version %s", job.KernelVersion)
				job.mu.Unlock()
				log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
				return
			}
		} else {
			log.Printf("[DEBUG] [job:%s] Found build directory in alternative location", jobID)
		}
	} else {
		log.Printf("[DEBUG] [job:%s] Found build directory in standard location", jobID)
	}

	// Verify Makefile exists
	makefilePath := filepath.Join(buildDir, "Makefile")
	log.Printf("[DEBUG] [job:%s] Verifying kernel Makefile exists: %s", jobID, makefilePath)
	if _, err := os.Stat(makefilePath); os.IsNotExist(err) {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Kernel Makefile not found in %s", buildDir)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}
	log.Printf("[INFO] [job:%s] Using kernel build directory: %s", jobID, buildDir)

	// Prepare DRBD source for this build (create isolated copy)
	log.Printf("[INFO] [job:%s] Preparing DRBD source (isolated copy for this build)...", jobID)
	drbdBuildDir := filepath.Join(tmpDir, "drbd")
	if err := s.prepareDRBDForBuild(drbdBuildDir, jobID); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to prepare DRBD source: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}
	log.Printf("[DEBUG] [job:%s] DRBD source prepared at: %s", jobID, drbdBuildDir)

	// Cleanup DRBD copy after build (unless keepTmpDir is set)
	if !s.keepTmpDir {
		defer func() {
			log.Printf("[DEBUG] [job:%s] Cleaning up DRBD build directory: %s", jobID, drbdBuildDir)
			if err := os.RemoveAll(drbdBuildDir); err != nil {
				log.Printf("[WARNING] [job:%s] Failed to remove DRBD build directory: %v", jobID, err)
			}
		}()
	}

	// Build DRBD module
	modulesDir := filepath.Join(tmpDir, "modules")
	log.Printf("[DEBUG] [job:%s] Creating modules output directory: %s", jobID, modulesDir)
	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create modules directory: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}

	log.Printf("[INFO] [job:%s] Starting DRBD module build process...", jobID)
	if err := s.buildDRBD(job.KernelVersion, buildDir, modulesDir, drbdBuildDir, jobID); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = err.Error()
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] Build failed: %s", jobID, err.Error())
		return
	}
	log.Printf("[DEBUG] [job:%s] DRBD module build completed successfully", jobID)

	// Collect .ko files
	log.Printf("[DEBUG] [job:%s] Searching for .ko files in %s...", jobID, modulesDir)
	koFiles, err := findKOFiles(modulesDir)
	if err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to find .ko files: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}

	log.Printf("[DEBUG] [job:%s] Found %d .ko file(s)", jobID, len(koFiles))
	if len(koFiles) == 0 {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("No .ko files found after build in %s", modulesDir)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}

	for i, koFile := range koFiles {
		log.Printf("[DEBUG] [job:%s]   [%d] %s", jobID, i+1, koFile)
	}

	// Create tar.gz archive with .ko files and save to cache
	cachePath := job.CachePath
	log.Printf("[DEBUG] [job:%s] Creating cache file: %s", jobID, cachePath)
	cacheFile, err := os.Create(cachePath)
	if err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create cache file: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}
	defer cacheFile.Close()

	log.Printf("[DEBUG] [job:%s] Creating tar.gz archive with %d .ko file(s)...", jobID, len(koFiles))
	if err := createTarGz(cacheFile, koFiles, modulesDir); err != nil {
		os.Remove(cachePath)
		job.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("Failed to create tar.gz: %v", err)
		job.mu.Unlock()
		log.Printf("[ERROR] [job:%s] %s", jobID, job.Error)
		return
	}

	cacheFile.Close()
	cacheInfo, _ := os.Stat(cachePath)
	if cacheInfo != nil {
		log.Printf("[DEBUG] [job:%s] Cache file created successfully: %d bytes", jobID, cacheInfo.Size())
	}

	// Update job status
	job.mu.Lock()
	job.Status = StatusCompleted
	job.mu.Unlock()
	log.Printf("[INFO] [job:%s] Successfully built DRBD modules for kernel %s (cache: %s, size: %d bytes)",
		jobID, job.KernelVersion, cachePath, cacheInfo.Size())
}

func (s *server) getStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		vars := mux.Vars(r)
		jobID := vars["job_id"]
		log.Printf("[DEBUG] [%s] Status request for job: %s", remoteAddr, jobID)

		s.jmu.RLock()
		job, exists := s.jobs[jobID]
		s.jmu.RUnlock()

		if !exists {
			log.Printf("[DEBUG] [%s] Job not found: %s", remoteAddr, jobID)
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
		log.Printf("[DEBUG] [%s] Job status: %s, kernel=%s, duration=%s, error=%v",
			remoteAddr, status, kernelVersion, duration, errorMsg != "")

		resp := BuildResponse{
			Status: status,
			Error:  errorMsg,
		}

		if status == StatusCompleted {
			resp.DownloadURL = fmt.Sprintf("/api/v1/download/%s", jobID)
			log.Printf("[DEBUG] [%s] Job completed, download_url: %s", remoteAddr, resp.DownloadURL)
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

		json.NewEncoder(w).Encode(respData)
	}
}

func (s *server) downloadModule() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		vars := mux.Vars(r)
		jobID := vars["job_id"]
		log.Printf("[DEBUG] [%s] Download request for job: %s", remoteAddr, jobID)

		s.jmu.RLock()
		job, exists := s.jobs[jobID]
		s.jmu.RUnlock()

		if !exists {
			log.Printf("[DEBUG] [%s] Job not found: %s", remoteAddr, jobID)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Job not found: %s", jobID)
			return
		}

		job.mu.RLock()
		status := job.Status
		cachePath := job.CachePath
		kernelVersion := job.KernelVersion
		job.mu.RUnlock()

		log.Printf("[DEBUG] [%s] Job status: %s, cache_path: %s", remoteAddr, status, cachePath)

		if status != StatusCompleted {
			log.Printf("[DEBUG] [%s] Job not completed, status: %s", remoteAddr, status)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "Job is not completed yet. Status: %s", status)
			return
		}

		if cachePath == "" {
			log.Printf("[DEBUG] [%s] Cache path not set for completed job", remoteAddr)
			s.errorf(http.StatusInternalServerError, remoteAddr, w, "Cache path not set for completed job")
			return
		}

		cacheInfo, err := os.Stat(cachePath)
		if os.IsNotExist(err) {
			log.Printf("[DEBUG] [%s] Cache file not found: %s", remoteAddr, cachePath)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Cache file not found: %s", cachePath)
			return
		}

		log.Printf("[INFO] [%s] Serving module file: %s (size: %d bytes)", remoteAddr, cachePath, cacheInfo.Size())
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", kernelVersion))
		http.ServeFile(w, r, cachePath)
		log.Printf("[DEBUG] [%s] Module file sent successfully", remoteAddr)
	}
}

func (s *server) buildDRBD(kernelVersion, kernelBuildDir, outputDir, drbdDir, jobID string) error {
	log.Printf("[DEBUG] [job:%s] Starting buildDRBD: kernel=%s, kdir=%s, output=%s, drbd=%s", jobID, kernelVersion, kernelBuildDir, outputDir, drbdDir)

	// Change to DRBD directory
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)

	log.Printf("[DEBUG] [job:%s] Changing to DRBD directory: %s", jobID, drbdDir)
	if err := os.Chdir(drbdDir); err != nil {
		return fmt.Errorf("failed to change to DRBD directory %s: %v", drbdDir, err)
	}

	// Verify DRBD Makefile exists
	log.Printf("[DEBUG] [job:%s] Verifying DRBD Makefile exists...", jobID)
	if _, err := os.Stat("Makefile"); os.IsNotExist(err) {
		return fmt.Errorf("DRBD Makefile not found in %s", drbdDir)
	}
	log.Printf("[DEBUG] [job:%s] DRBD Makefile found", jobID)

	// Set environment variables for make
	// env := os.Environ() // TODO: check if outer ENV is needed
	env := []string{}
	env = append(env, fmt.Sprintf("KVER=%s", kernelVersion))
	env = append(env, fmt.Sprintf("KDIR=%s", kernelBuildDir))
	env = append(env, fmt.Sprintf("SPAAS_URL=%s", s.spaasURL))
	env = append(env, "SPAAS=true")
	log.Printf("[DEBUG] [job:%s] Environment: KVER=%s, KDIR=%s, SPAAS_URL=%s", jobID, kernelVersion, kernelBuildDir, s.spaasURL)

	// Clean previous build
	log.Printf("[DEBUG] [job:%s] Running 'make clean'...", jobID)
	cmd := exec.Command("make", "clean")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = drbdDir
	if err := cmd.Run(); err != nil {
		// Don't fail on clean errors
		log.Printf("[DEBUG] [job:%s] WARNING: make clean failed (ignored): %v", jobID, err)
	} else {
		log.Printf("[DEBUG] [job:%s] 'make clean' completed successfully", jobID)
	}

	// Check and update submodules if needed (required by make module)
	// TODO: check
	log.Printf("[DEBUG] [job:%s] Checking submodules...", jobID)
	cmd = exec.Command("make", "check-submods")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = drbdDir
	if err := cmd.Run(); err != nil {
		log.Printf("[DEBUG] [job:%s] WARNING: make check-submods failed (may be OK if no submodules): %v", jobID, err)
	} else {
		log.Printf("[DEBUG] [job:%s] Submodules check completed", jobID)
	}

	// Build module
	log.Printf("[INFO] [job:%s] Running 'make module' (this may take several minutes)...", jobID)
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
	log.Printf("[DEBUG] [job:%s] 'make module' completed successfully in %v", jobID, buildDuration)

	// Install modules to output directory
	log.Printf("[DEBUG] [job:%s] Installing modules to %s...", jobID, outputDir)
	cmd = exec.Command("make", "install")
	cmd.Env = append(env, fmt.Sprintf("DESTDIR=%s", outputDir))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = drbdDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make install failed: %v", err)
	}
	log.Printf("[DEBUG] [job:%s] Modules installed successfully to %s", jobID, outputDir)

	return nil
}

// prepareDRBDForBuild prepares an isolated DRBD source directory for a build.
// It either copies from base drbdDir or clones from repository if version is specified.
func (s *server) prepareDRBDForBuild(destDir, jobID string) error {
	log.Printf("[DEBUG] [job:%s] Preparing DRBD source at: %s", jobID, destDir)

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create DRBD build directory: %v", err)
	}

	// Check if base DRBD directory exists
	if s.drbdDir != "" {
		if info, err := os.Stat(s.drbdDir); err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(s.drbdDir, "Makefile")); err == nil {
				// Copy existing DRBD directory
				log.Printf("[INFO] [job:%s] Copying DRBD source from %s to %s...", jobID, s.drbdDir, destDir)
				if err := copyDirectory(s.drbdDir, destDir, jobID); err != nil {
					return fmt.Errorf("failed to copy DRBD directory: %v", err)
				}
				log.Printf("[DEBUG] [job:%s] DRBD source copied successfully", jobID)
				return nil
			}
		}
	}

	// If no base directory or version specified, clone from repository
	if s.drbdVersion == "" {
		return fmt.Errorf("no DRBD source available: neither drbd-dir exists nor drbd-version specified")
	}

	log.Printf("[INFO] [job:%s] Cloning DRBD repository (version=%s)...", jobID, s.drbdVersion)
	if err := s.cloneDRBDRepoToDir(s.drbdVersion, s.drbdRepo, destDir, jobID); err != nil {
		return fmt.Errorf("failed to clone DRBD repository: %v", err)
	}
	log.Printf("[DEBUG] [job:%s] DRBD repository cloned successfully", jobID)
	return nil
}

// copyDirectory recursively copies a directory from src to dst
// TODO: review
func copyDirectory(src, dst, jobID string) error {
	log.Printf("[DEBUG] [job:%s] Copying directory: %s -> %s", jobID, src, dst)

	err := filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Calculate relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		// Copy file
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, srcFile)
		return err
	})

	if err != nil {
		log.Printf("[ERROR] [job:%s] Failed to copy directory: %v", jobID, err)
		return err
	}

	log.Printf("[DEBUG] [job:%s] Directory copied successfully", jobID)
	return nil
}

// cloneDRBDRepoToDir clones the DRBD repository to the specified directory
func (s *server) cloneDRBDRepoToDir(version, repoURL, destDir, jobID string) error {
	log.Printf("[DEBUG] [job:%s] Cloning DRBD repository: version=%s, repo=%s, dest=%s", jobID, version, repoURL, destDir)

	// Determine branch name (format: drbd-9.2.12)
	branch := fmt.Sprintf("drbd-%s", version)
	log.Printf("[DEBUG] [job:%s] Cloning branch: %s", jobID, branch)

	// Clone repository directly to destination
	log.Printf("[DEBUG] [job:%s] Running: git clone --depth 1 --branch %s %s %s", jobID, branch, repoURL, destDir)
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", branch, repoURL, destDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %v", err)
	}

	gitHash := "123456" // TODO: git rev-parse HEAD

	// Update submodules
	log.Printf("[DEBUG] [job:%s] Updating submodules...", jobID)
	cmd = exec.Command("git", "submodule", "update", "--init", "--recursive")
	cmd.Dir = destDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git submodule update failed: %v", err)
	}

	// Remove .git directory
	log.Printf("[DEBUG] [job:%s] Removing .git directory...", jobID)
	if err := os.RemoveAll(filepath.Join(destDir, ".git")); err != nil {
		log.Printf("[WARNING] [job:%s] Failed to remove .git directory: %v", jobID, err)
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
	log.Printf("[DEBUG] [job:%s] Created drbd/.drbd_git_revision with hash: %s", jobID, gitHash)

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
	log.Printf("[%s] ERROR [%d]: %s", remoteAddr, code, msg)
}
