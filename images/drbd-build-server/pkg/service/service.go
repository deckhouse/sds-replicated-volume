package service

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/internal/utils"
	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/pkg/model"
)

type JobInfo struct {
	Key           string
	KernelVersion string
	Status        model.BuildStatus
	Error         string
	CreatedAt     time.Time
	CompletedAt   *time.Time
	CachePath     string
}

type BuildJob struct {
	JobInfo
	mu sync.RWMutex
}

type JobsRepository map[string]*BuildJob

type BuildService struct {
	jmu      sync.RWMutex
	jobsRepo JobsRepository

	drbdDir    string // Base DRBD directory (if pre-existing)
	cacheDir   string
	keepTmpDir bool
	spaasURL   string
	logger     *slog.Logger // Structured logger

	// DRBD source configuration
	drbdVersion string // DRBD version to clone (if needed)
	drbdRepo    string // DRBD repository URL
}

func NewTestBuildService(cacheDir *string, logger *slog.Logger) *BuildService {

	return &BuildService{
		cacheDir: *cacheDir,
		logger:   logger,
		jobsRepo: make(map[string]*BuildJob),
	}
}

func NewBuildService(
	drbdDir *string, // Base DRBD directory (if pre-existing)
	cacheDir *string,
	keepTmpDir *bool,
	spaasURL *string,
	logger *slog.Logger, // Structured logger
	drbdVersion *string, // DRBD version to clone (if needed)
	drbdRepo *string, // DRBD repository URL
) *BuildService {
	return &BuildService{
		drbdDir:     *drbdDir, // Base directory (if exists, will be copied)
		cacheDir:    *cacheDir,
		keepTmpDir:  *keepTmpDir,
		spaasURL:    *spaasURL,
		logger:      logger,
		jobsRepo:    make(map[string]*BuildJob),
		drbdVersion: *drbdVersion,
		drbdRepo:    *drbdRepo,
	}
}

func (s *BuildService) createCachePath(cacheKey string) string {
	return filepath.Join(s.cacheDir, cacheKey+".tar.gz")
}

func (s *BuildService) GetCached(cacheKey string, kernelVersion string, remoteAddr string) *string {
	// Check if already in cache
	cachePath := s.createCachePath(cacheKey)
	s.logger.Debug("Checking cache", "remote_addr", remoteAddr, "cache_path", cachePath)

	info, err := os.Stat(cachePath)
	if err != nil {
		s.logger.Debug("Cache miss", "remote_addr", remoteAddr, "reason", "file not found or error", "error", err)
		return nil
	}
	if info == nil {
		return nil
	}

	if info.Size() == 0 {
		s.logger.Debug("Cache miss", "remote_addr", remoteAddr, "reason", "file exists but size is 0")
		return nil
	}

	s.logger.Info("Cache HIT", "remote_addr", remoteAddr, "kernel_version", kernelVersion, "cache_key", cacheKey[:16], "size_bytes", info.Size())
	s.logger.Debug("Serving cached module file", "remote_addr", remoteAddr)
	return &cachePath
}

// return true if job exist and jobsRepo count
func (s *BuildService) JobInfo(cacheKey string) (JobInfo, int) {
	s.jmu.RLock()
	defer s.jmu.RUnlock()

	job, exists := s.jobsRepo[cacheKey]
	if !exists {
		return JobInfo{Status: model.StatusNotExist}, len(s.jobsRepo)
	}

	return JobInfo{
		Status:    job.Status,
		Key:       job.Key,
		Error:     job.Error,
		CreatedAt: job.CreatedAt,
		CachePath: job.CachePath,
	}, len(s.jobsRepo)
}

// return true if job exist and jobsRepo count
func (s *BuildService) GetJob(id string) JobInfo {
	s.jmu.RLock()
	defer s.jmu.RUnlock()

	job, exists := s.jobsRepo[id]
	if !exists {
		return JobInfo{Status: model.StatusNotExist}
	}

	return JobInfo{
		Status:    job.Status,
		Key:       job.Key,
		Error:     job.Error,
		CreatedAt: job.CreatedAt,
		CachePath: job.CachePath,
	}
}

func (s *BuildService) CreateJob(cacheKey string, kernelVersion string, headersData []byte, remoteAddr string) {

	cachePath := s.createCachePath(cacheKey)

	job := &BuildJob{
		JobInfo: JobInfo{
			Key:           cacheKey,
			KernelVersion: kernelVersion,
			Status:        model.StatusPending,
			CreatedAt:     time.Now(),
			CachePath:     cachePath,
		},
	}

	s.jmu.Lock()
	s.jobsRepo[cacheKey] = job
	activeJobsCount := len(s.jobsRepo)
	s.jmu.Unlock()
	s.logger.Info("Created new build job", "remote_addr", remoteAddr, "job_id", cacheKey[:16], "kernel_version", kernelVersion, "cache_path", cachePath, "total_jobs", activeJobsCount)
	s.logger.Info("Starting DRBD build", "remote_addr", remoteAddr, "kernel_version", kernelVersion, "job_id", cacheKey[:16])

	// Start build in background
	s.logger.Debug("Launching async build goroutine", "remote_addr", remoteAddr, "job_id", cacheKey[:16])
	go s.buildModule(job, headersData)
}

func (s *BuildService) AddJob(job *BuildJob) error {
	s.jmu.Lock()
	defer s.jmu.Unlock()
	if _, ok := s.jobsRepo[job.Key]; ok {
		return errors.New("job already exists")
	}
	s.jobsRepo[job.Key] = job
	return nil
}

func (s *BuildService) JobsCount() int {
	s.jmu.RLock()
	defer s.jmu.RUnlock()
	return len(s.jobsRepo)
}

func (s *BuildService) buildModule(job *BuildJob, headersData []byte) {
	jobID := job.Key[:16]
	s.logger.Debug("Async build started", "job_id", jobID, "kernel_version", job.KernelVersion)

	job.mu.Lock()
	job.Status = model.StatusBuilding
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
	tmpDir, err := os.MkdirTemp("", "drbd-build-BuildServer-*")
	if err != nil {
		job.mu.Lock()
		job.Status = model.StatusFailed
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
		job.Status = model.StatusFailed
		job.Error = fmt.Sprintf("Failed to create kernel headers directory: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	// Extract tar.gz archive
	s.logger.Debug("Extracting kernel headers archive", "job_id", jobID, "bytes", len(headersData))
	headersReader := bytes.NewReader(headersData)
	if err := utils.ExtractTarGz(headersReader, kernelHeadersDir); err != nil {
		job.mu.Lock()
		job.Status = model.StatusFailed
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
		job.Status = model.StatusFailed
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
		job.Status = model.StatusFailed
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
		job.Status = model.StatusFailed
		job.Error = fmt.Sprintf("Failed to create modules directory: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	s.logger.Info("Starting DRBD module build process", "job_id", jobID)
	if err := s.buildDRBD(job.KernelVersion, buildDir, modulesDir, drbdBuildDir, jobID); err != nil {
		job.mu.Lock()
		job.Status = model.StatusFailed
		job.Error = err.Error()
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", err.Error())
		return
	}
	s.logger.Debug("DRBD module build completed successfully", "job_id", jobID)

	// Collect .ko files
	s.logger.Debug("Searching for .ko files", "job_id", jobID, "modules_dir", modulesDir)
	koFiles, err := utils.FindKOFiles(modulesDir)
	if err != nil {
		job.mu.Lock()
		job.Status = model.StatusFailed
		job.Error = fmt.Sprintf("Failed to find .ko files: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	s.logger.Debug("Found .ko files", "job_id", jobID, "count", len(koFiles))
	if len(koFiles) == 0 {
		job.mu.Lock()
		job.Status = model.StatusFailed
		job.Error = fmt.Sprintf("No .ko files found after build in %s", modulesDir)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}

	s.logger.Debug("Found .ko files", "job_id", jobID, "files", koFiles)

	// Create tar.gz archive with .ko files and save to cache
	cachePath := job.CachePath
	s.logger.Debug("Creating cache file", "job_id", jobID, "cache_path", cachePath)
	cacheFile, err := os.Create(cachePath)
	if err != nil {
		job.mu.Lock()
		job.Status = model.StatusFailed
		job.Error = fmt.Sprintf("Failed to create cache file: %v", err)
		job.mu.Unlock()
		s.logger.Error("Build failed", "job_id", jobID, "error", job.Error)
		return
	}
	defer cacheFile.Close()

	s.logger.Debug("Creating tar.gz archive", "job_id", jobID, "ko_files_count", len(koFiles))
	if err := utils.CreateTarGz(cacheFile, koFiles, modulesDir); err != nil {
		os.Remove(cachePath)
		job.mu.Lock()
		job.Status = model.StatusFailed
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
	job.Status = model.StatusCompleted
	job.mu.Unlock()
	s.logger.Info("Successfully built DRBD modules", "job_id", jobID, "kernel_version", job.KernelVersion, "cache_path", cachePath, "size_bytes", cacheInfo.Size())
}

func (s *BuildService) buildDRBD(kernelVersion, kernelBuildDir, outputDir, drbdDir, jobID string) error {
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
	env := append(
		os.Environ(),
		fmt.Sprintf("KVER=%s", kernelVersion),
		fmt.Sprintf("KDIR=%s", kernelBuildDir),
		fmt.Sprintf("SPAAS_URL=%s", s.spaasURL),
		fmt.Sprintf("DESTDIR=%s", outputDir),
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

	s.logger.Debug("MkdirAll", "job_id", jobID, "duration", buildDuration)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create outputDir: %v", err)
	}
	// Install modules to output directory
	s.logger.Debug("Installing modules to output directory", "job_id", jobID, "output_dir", outputDir)
	cmd = exec.Command(
		"make",
		"install",
		fmt.Sprintf("DESTDIR=%s", outputDir),
		fmt.Sprintf("KVER=%s", kernelVersion),
		fmt.Sprintf("KDIR=%s", kernelBuildDir),
	)

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
func (s *BuildService) findKernelBuildDir(kernelHeadersDir, kernelVersion, jobID string) (string, error) {
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
		//TODO: err!= nil handle critical errors
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
		//TODO: err!= nil handle critical errors
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
func (s *BuildService) prepareDRBDForBuild(destDir, jobID string) error {
	s.logger.Debug("Preparing DRBD source", "job_id", jobID, "dest_dir", destDir)

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create DRBD build directory: %w", err)
	}

	// Check if base DRBD directory exists
	if s.drbdDir != "" {
		if info, err := os.Stat(s.drbdDir); err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(s.drbdDir, "Makefile")); err == nil {
				// Copy existing DRBD directory
				s.logger.Info("Copying DRBD source", "job_id", jobID, "src", s.drbdDir, "dst", destDir)
				if err := utils.CopyDirectory(s.drbdDir, destDir, jobID, s.logger); err != nil {
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

// cloneDRBDRepoToDir clones the DRBD repository to the specified directory
func (s *BuildService) cloneDRBDRepoToDir(version, repoURL, destDir, jobID string) error {
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
