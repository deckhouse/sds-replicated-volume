package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/internal/utils"
	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/pkg/model"
	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/pkg/service"
	"github.com/gorilla/mux"
)

const (
	buildPath     = "/api/v1/build"
	jobStatusPath = "/api/v1/status/{job_id}"
	downloadPath  = "/api/v1/download/{job_id}"
	helloPath     = "/api/v1/hello"
)

func (s *BuildServer) replyFile(path string, kernelVersion *string, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", *kernelVersion))
	http.ServeFile(w, r, path)
}

func (s *BuildServer) replyAccepted(w http.ResponseWriter, resp *model.BuildResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("Failed to encode response", "error", err)
	}
	return
}

func (s *BuildServer) replyOk(w http.ResponseWriter, resp *model.BuildResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("Failed to encode response", "error", err)
	}
	return
}

type BuildServer struct {
	router       *mux.Router
	logger       *slog.Logger // Structured logger
	maxBytesBody int64
	version      string
	buildService *service.BuildService
}

func (s *BuildServer) readKernelHeadersFromBody(r *http.Request, remoteAddr string) ([]byte, error) {
	// Read headers data for cache key generation
	body := r.Body
	s.logger.Debug("Reading kernel headers from request body", "remote_addr", remoteAddr)
	headersData, err := io.ReadAll(body) // TODO: read max bytes
	if err != nil {
		s.logger.Error("Failed to read request body", "remote_addr", remoteAddr, "error", err)
		return nil, errors.New("kernel_version parameter or X-Kernel-Version header is required")
	}
	s.logger.Debug("Read kernel headers data", "remote_addr", remoteAddr, "bytes", len(headersData))
	return headersData, nil
}

func getKernelVersion(r *http.Request, s *BuildServer, remoteAddr string) (string, error) {
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
		return "", errors.New("kernel_version parameter or X-Kernel-Version header is required")
	}

	// Validate kernel version format
	// Kernel version format: X.Y.Z[-flavor] or X.Y.Z[-flavor]-build
	// Examples: 5.15.0, 5.15.0-generic, 5.15.0-86-generic
	kernelVersionRegex := regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9_-]+)?(-[0-9]+)?$`)
	if !kernelVersionRegex.MatchString(kernelVersion) {
		s.logger.Error("Invalid kernel version format", "remote_addr", remoteAddr, "kernel_version", kernelVersion)
		return "", errors.New("Invalid kernel version format. Expected format: X.Y.Z[-flavor] or X.Y.Z[-flavor]-build")

	}

	return kernelVersion, nil
}

func NewBuildServer(
	drbdDir *string, // Base DRBD directory (if pre-existing)
	cacheDir *string,
	maxBytesBody *int64,
	keepTmpDir *bool,
	spaasURL *string,
	logger *slog.Logger, // Structured logger
	drbdVersion *string, // DRBD version to clone (if needed)
	drbdRepo *string, // DRBD repository URL
	version string,
) *BuildServer {
	server := &BuildServer{
		router:       mux.NewRouter(),
		logger:       logger,
		version:      version,
		maxBytesBody: *maxBytesBody,
		buildService: service.NewBuildService(
			drbdDir, // Base DRBD directory (if pre-existing)
			cacheDir,
			keepTmpDir,
			spaasURL,
			logger,      // Structured logger
			drbdVersion, // DRBD version to clone (if needed)
			drbdRepo,
		),
	}
	server.registerRoutes()
	return server
}

// handler interface, wrapped for MaxBytesReader
func (s *BuildServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBytesBody)
	s.router.ServeHTTP(w, r)
}

func (s *BuildServer) registerRoutes() {
	s.router.HandleFunc(buildPath, s.buildModuleHandler()).Methods("POST")
	s.router.HandleFunc(jobStatusPath, s.getStatusHandler()).Methods("GET")
	s.router.HandleFunc(downloadPath, s.downloadModule()).Methods("GET")
	s.router.HandleFunc(helloPath, s.helloHandler()).Methods("GET")
}

func (s *BuildServer) errorf(code int, remoteAddr string, w http.ResponseWriter, format string, a ...interface{}) {
	w.WriteHeader(code)
	msg := fmt.Sprintf(format, a...)
	_, _ = fmt.Fprint(w, msg)
	s.logger.Error("HTTP error", "remote_addr", remoteAddr, "code", code, "error", msg)
}

func (s *BuildServer) error(code int, remoteAddr string, w http.ResponseWriter, error error) {
	w.WriteHeader(code)
	s.logger.Error("HTTP error", "remote_addr", remoteAddr, "code", code, "error", error)
}

func (s *BuildServer) helloHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := fmt.Fprintf(w, "Successfully connected to DRBD Build Server ('%s')\n", s.version); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func (s *BuildServer) buildModuleHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		s.logger.Debug("Received build request", "remote_addr", remoteAddr, "method", r.Method, "path", r.URL.Path)

		kernelVersion, err := getKernelVersion(r, s, remoteAddr)
		if err != nil {
			s.error(http.StatusBadRequest, remoteAddr, w, err)
			return
		}
		headersData, err := s.readKernelHeadersFromBody(r, remoteAddr)
		if err != nil {
			s.error(http.StatusBadRequest, remoteAddr, w, err)
			return
		}

		// Generate cache key
		cacheKey := utils.GenerateCacheKey(kernelVersion, headersData)
		s.logger.Debug("Generated cache key", "remote_addr", remoteAddr, "cache_key", cacheKey)

		//check cache
		if cachePath := s.buildService.GetCached(cacheKey, kernelVersion, remoteAddr); cachePath != nil {
			s.logger.Debug("Serving cached module file", "remote_addr", remoteAddr)
			s.replyFile(*cachePath, &kernelVersion, w, r)
			s.logger.Debug("Successfully sent cached module to client", "remote_addr", remoteAddr)
			return
		}

		// Check if build is in progress
		s.logger.Debug("Checking for existing job", "remote_addr", remoteAddr, "cache_key", cacheKey[:16])
		//TODO check why active?
		info, activeJobsCount := s.buildService.JobInfo(cacheKey)
		s.logger.Debug("Total active jobsRepo", "remote_addr", remoteAddr, "count", activeJobsCount)

		switch info.Status {
		case model.StatusNotExist:
			s.logger.Debug("No existing job found, will create new one", "remote_addr", remoteAddr)
			s.buildService.CreateJob(cacheKey, kernelVersion, headersData, remoteAddr)
			s.logger.Debug("Returning job ID to client with status 202 Accepted", "remote_addr", remoteAddr, "job_id", cacheKey[:16])
			s.replyAccepted(w, &model.BuildResponse{
				Status: model.StatusBuilding,
				JobID:  cacheKey,
			},
			)
			return
		case model.StatusFailed:
			s.logger.Debug("Previous job failed, creating new one", "remote_addr", remoteAddr, "error", info.Error)
			s.buildService.CreateJob(cacheKey, kernelVersion, headersData, remoteAddr)
			s.replyAccepted(w, &model.BuildResponse{
				Status: model.StatusBuilding,
				JobID:  cacheKey,
				Error:  "Previous job failed, creating new one",
			},
			)
		case model.StatusBuilding, model.StatusPending:
			s.logger.Debug("Found existing job", "remote_addr", remoteAddr, "job_id", info.Key[:16], "status", info.Status, "created_at", info.CreatedAt.Format(time.RFC3339))
			s.logger.Info(fmt.Sprintf("Build status is %s", info.Status), "remote_addr", remoteAddr, "kernel_version", kernelVersion, "job_id", info.Key[:16], "status", info.Status)
			s.replyAccepted(w, &model.BuildResponse{
				Status: info.Status,
				JobID:  info.Key,
			},
			)
			return
		case model.StatusCompleted:
			s.logger.Debug("Job completed, checking cache file", "remote_addr", remoteAddr, "cache_path", info.CachePath)
			buildError := ""
			if info.CachePath != "" {
				if statInfo, err := os.Stat(info.CachePath); err == nil {
					s.logger.Info("Serving completed build", "remote_addr", remoteAddr, "kernel_version", kernelVersion, "completed_at", info.CompletedAt, "size_bytes", statInfo.Size())
					s.replyOk(w, &model.BuildResponse{
						Status:      info.Status,
						JobID:       info.Key,
						DownloadURL: s.makeDownloadUrl(info.Key),
					},
					)
					s.logger.Debug("Successfully sent completed build to client", "remote_addr", remoteAddr)
					return
				}
				buildError = "Cache file for completed job not found"
			}
			buildError = "Cache path blank or empty"
			s.logger.Error(buildError, "remote_addr", remoteAddr, "error", err)
			s.buildService.CreateJob(cacheKey, kernelVersion, headersData, remoteAddr)
			s.logger.Debug("Returning job ID to client with status 202 Accepted", "remote_addr", remoteAddr, "job_id", cacheKey[:16])
			s.replyAccepted(w, &model.BuildResponse{
				Status: model.StatusBuilding,
				JobID:  cacheKey,
				Error:  "Previous job has an error, creating new one",
			},
			)
		}
	}
}

func (s *BuildServer) getStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		jobID := mux.Vars(r)["job_id"]
		s.logger.Debug("Status request for job", "remote_addr", remoteAddr, "job_id", jobID)

		job := s.buildService.GetJob(jobID)

		if job.Status == model.StatusNotExist {
			s.logger.Debug("Job not found", "remote_addr", remoteAddr, "job_id", jobID)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Job not found: %s", jobID)
			return
		}

		duration := ""
		if job.CompletedAt != nil {
			duration = job.CompletedAt.Sub(job.CreatedAt).String()
		} else {
			duration = time.Since(job.CreatedAt).String()
		}
		s.logger.Debug("Job status", "remote_addr", remoteAddr, "status", job.Status, "kernel_version", job.KernelVersion, "duration", duration, "has_error", job.Error != "")

		// Add extra info for debugging
		rs := model.StatusResponse{
			Status:        job.Status,
			JobID:         job.Key,
			KernelVersion: job.KernelVersion,
			CreatedAt:     job.CreatedAt.Format(time.RFC3339),
			Error:         job.Error,
			CachePath:     job.CachePath,
		}

		if job.CompletedAt != nil {
			rs.CompletedAt = job.CompletedAt.Format(time.RFC3339)
			rs.Duration = duration
		}

		w.Header().Set("Content-Type", "application/json")
		switch job.Status {
		case model.StatusCompleted:
			url := s.makeDownloadUrl(jobID)
			rs.DownloadURL = url
			s.logger.Debug("Job completed", "remote_addr", remoteAddr, "download_url", url)
			w.WriteHeader(http.StatusOK)
		case model.StatusFailed:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusAccepted)
		}

		if err := json.NewEncoder(w).Encode(rs); err != nil {
			s.logger.Error("Failed to encode body", "remote_addr", remoteAddr, "error", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func (s *BuildServer) makeDownloadUrl(jobID string) string {
	return fmt.Sprintf("/api/v1/download/%s", jobID)
}

func (s *BuildServer) downloadModule() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr
		vars := mux.Vars(r)
		jobID := vars["job_id"]
		s.logger.Debug("Download request for job", "remote_addr", remoteAddr, "job_id", jobID)

		job := s.buildService.GetJob(jobID)

		if job.Status == model.StatusNotExist {
			s.logger.Debug("Job not found", "remote_addr", remoteAddr, "job_id", jobID)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Job not found: %s", jobID)
			return
		}

		s.logger.Debug("Job status", "remote_addr", remoteAddr, "status", job.Status, "cache_path", job.CachePath)

		if job.Status != model.StatusCompleted {
			s.logger.Debug("Job not completed", "remote_addr", remoteAddr, "status", job.Status)
			s.errorf(http.StatusBadRequest, remoteAddr, w, "Job is not completed yet. Status: %s", job.Status)
			return
		}

		if job.CachePath == "" {
			s.logger.Debug("Cache path not set for completed job", "remote_addr", remoteAddr)
			s.errorf(http.StatusInternalServerError, remoteAddr, w, "Cache path not set for completed job")
			return
		}

		cacheInfo, err := os.Stat(job.CachePath)
		if os.IsNotExist(err) {
			s.logger.Debug("Cache file not found", "remote_addr", remoteAddr, "cache_path", job.CachePath)
			s.errorf(http.StatusNotFound, remoteAddr, w, "Cache file not found: %s", job.CachePath)
			return
		}

		s.logger.Info("Serving module file", "remote_addr", remoteAddr, "cache_path", job.CachePath, "size_bytes", cacheInfo.Size())
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"drbd-modules-%s.tar.gz\"", job.KernelVersion))
		http.ServeFile(w, r, job.CachePath)
		s.logger.Debug("Module file sent successfully", "remote_addr", remoteAddr)
	}
}
