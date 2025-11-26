package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/internal/utils"
	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/pkg/model"
	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/pkg/service"
	"github.com/gorilla/mux"
)

// newTestServer creates a test server with default logger level from flagLogLevel
func newTestServer(cacheDir *string) *BuildServer {
	dir := ""
	if cacheDir != nil {
		dir = *cacheDir
	}
	log := utils.InitLogger("info")
	return &BuildServer{
		router:  mux.NewRouter(),
		logger:  log,
		version: "version",
		buildService: service.NewTestBuildService(
			&dir,
			log,
		),
	}
}

func TestHelloEndpoint(t *testing.T) {
	s := newTestServer(nil)
	s.registerRoutes()

	req := httptest.NewRequest("GET", "/api/v1/hello", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Successfully connected") {
		t.Errorf("Expected 'Successfully connected' in response, got: %s", body)
	}
}

func TestBuildModuleEndpoint_NoKernelVersion(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer(&cacheDir)
	s.maxBytesBody = 100 * 1024 * 1024
	s.registerRoutes()

	req := httptest.NewRequest("POST", "/api/v1/build", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// TODO rewrite later
//func TestBuildModuleEndpoint_CacheHit(t *testing.T) {
//	cacheDir := t.TempDir()
//	s := newTestServer(&cacheDir)
//	s.maxBytesBody = 100 * 1024 * 1024
//	s.registerRoutes()
//
//	kernelVersion := "5.15.0-86-generic"
//	headersData := []byte("test headers")
//
//	// Create a minimal tar.gz archive for headers FIRST
//	var headersArchive bytes.Buffer
//	gzWriter := gzip.NewWriter(&headersArchive)
//	tarWriter := tar.NewWriter(gzWriter)
//	header := &tar.Header{
//		Name:     "test.txt",
//		Size:     int64(len(headersData)),
//		Mode:     0644,
//		Typeflag: tar.TypeReg,
//	}
//	if err := tarWriter.WriteHeader(header); err != nil {
//		t.Fatalf("Failed to write tar header: %v", err)
//	}
//	if _, err := tarWriter.Write(headersData); err != nil {
//		t.Fatalf("Failed to write tar content: %v", err)
//	}
//	err := tarWriter.Close()
//	if err != nil {
//		t.Fatalf("Failed to close tarWriter: %v", err)
//	}
//	err = gzWriter.Close()
//	if err != nil {
//		t.Fatalf("Failed to close gzWriter: %v", err)
//	}
//
//	// Now generate cache key from the actual archive bytes
//	archiveBytes := headersArchive.Bytes()
//	cacheKey := utils.GenerateCacheKey(kernelVersion, archiveBytes)
//	cachePath := filepath.Join(cacheDir, cacheKey+".tar.gz")
//
//	// Create a cached file
//	cachedContent := []byte("cached module content")
//	if err := os.WriteFile(cachePath, cachedContent, 0644); err != nil {
//		t.Fatalf("Failed to create cache file: %v", err)
//	}
//
//	// Reset archive buffer and recreate (since it was consumed)
//	headersArchive.Reset()
//	gzWriter = gzip.NewWriter(&headersArchive)
//	tarWriter = tar.NewWriter(gzWriter)
//	if err := tarWriter.WriteHeader(header); err != nil {
//		t.Fatalf("Failed to write tar header: %v", err)
//	}
//	if _, err := tarWriter.Write(headersData); err != nil {
//		t.Fatalf("Failed to write tar content: %v", err)
//	}
//	err = tarWriter.Close()
//	if err != nil {
//		t.Fatalf("Failed to close tarWriter: %v", err)
//	}
//	err = gzWriter.Close()
//	if err != nil {
//		t.Fatalf("Failed to close gzWriter: %v", err)
//	}
//
//	req := httptest.NewRequest("POST", "/api/v1/build?kernel_version="+kernelVersion, &headersArchive)
//	req.Header.Set("Content-Type", "application/gzip")
//	w := httptest.NewRecorder()
//	s.router.ServeHTTP(w, req)
//
//	if w.Code != http.StatusOK {
//		t.Errorf("Expected status 200 for cache hit, got %d", w.Code)
//	}
//
//	// Verify response contains cached content
//	if !bytes.Equal(w.Body.Bytes(), cachedContent) {
//		t.Error("Response body should contain cached content")
//	}
//}

func TestGetStatusEndpoint(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer(&cacheDir)
	s.registerRoutes()

	// Create a test job
	job := &service.BuildJob{
		JobInfo: service.JobInfo{
			Key:           "test-job-id",
			KernelVersion: "5.15.0-86-generic",
			Status:        model.StatusBuilding,
			CreatedAt:     time.Now(),
			CachePath:     filepath.Join(cacheDir, "test.tar.gz"),
		},
	}

	if err := s.buildService.AddJob(job); err != nil {
		t.Errorf("Job failed with error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/status/test-job-id", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status 202, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["status"] != "building" {
		t.Errorf("Expected status 'building', got %v", resp["status"])
	}
}

func TestGetStatusEndpoint_NotFound(t *testing.T) {
	s := newTestServer(nil)
	s.registerRoutes()

	req := httptest.NewRequest("GET", "/api/v1/status/non-existent-job", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestDownloadModuleEndpoint_NotCompleted(t *testing.T) {
	s := newTestServer(nil)
	s.registerRoutes()

	job := &service.BuildJob{
		JobInfo: service.JobInfo{
			Key:           "test-job-id",
			KernelVersion: "5.15.0-86-generic",
			Status:        model.StatusBuilding,
			CreatedAt:     time.Now(),
		},
	}
	if err := s.buildService.AddJob(job); err != nil {
		t.Errorf("Job failed with error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/download/test-job-id", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestDownloadModuleEndpoint_Completed(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer(&cacheDir)
	s.registerRoutes()

	cachePath := filepath.Join(cacheDir, "test-modules.tar.gz")
	cachedContent := []byte("cached module tar.gz content")
	if err := os.WriteFile(cachePath, cachedContent, 0644); err != nil {
		t.Fatalf("Failed to create cache file: %v", err)
	}

	job := &service.BuildJob{
		JobInfo: service.JobInfo{
			Key:           "test-job-id",
			KernelVersion: "5.15.0-86-generic",
			Status:        model.StatusCompleted,
			CreatedAt:     time.Now(),
			CachePath:     cachePath,
		},
	}
	if err := s.buildService.AddJob(job); err != nil {
		t.Errorf("Job failed with error: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/download/test-job-id", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if !bytes.Equal(w.Body.Bytes(), cachedContent) {
		t.Error("Response body should contain cached module content")
	}
}

func TestBuildModuleEndpoint_RaceCondition(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer(&cacheDir)
	s.maxBytesBody = 100 * 1024 * 1024
	s.registerRoutes()

	kernelVersion := "5.15.0-86-generic"
	headersData := []byte("test headers")

	// Create minimal tar.gz FIRST
	var headersArchive bytes.Buffer
	gzWriter := gzip.NewWriter(&headersArchive)
	tarWriter := tar.NewWriter(gzWriter)
	header := &tar.Header{
		Name:     "test.txt",
		Size:     int64(len(headersData)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("Failed to write tar header: %v", err)
	}
	if _, err := tarWriter.Write(headersData); err != nil {
		t.Fatalf("Failed to write tar content: %v", err)
	}
	err := tarWriter.Close()
	if err != nil {
		t.Fatalf("Failed to close tarWriter: %v", err)
	}
	err = gzWriter.Close()
	if err != nil {
		t.Fatalf("Failed to close gzWriter: %v", err)
	}

	// Generate cache key from actual archive bytes
	archiveBytes := headersArchive.Bytes()
	cacheKey := utils.GenerateCacheKey(kernelVersion, archiveBytes)

	// Create a job that's already building
	job := &service.BuildJob{
		JobInfo: service.JobInfo{
			Key:           cacheKey,
			KernelVersion: kernelVersion,
			Status:        model.StatusBuilding,
			CreatedAt:     time.Now(),
		},
	}
	if err := s.buildService.AddJob(job); err != nil {
		t.Errorf("Job failed with error: %v", err)
	}

	// Reset archive buffer and recreate
	headersArchive.Reset()
	gzWriter = gzip.NewWriter(&headersArchive)
	tarWriter = tar.NewWriter(gzWriter)
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("Failed to write tar header: %v", err)
	}
	if _, err := tarWriter.Write(headersData); err != nil {
		t.Fatalf("Failed to write tar content: %v", err)
	}
	err = tarWriter.Close()
	if err != nil {
		t.Fatalf("Failed to close tarWriter: %v", err)
	}
	err = gzWriter.Close()
	if err != nil {
		t.Fatalf("Failed to close gzWriter: %v", err)
	}

	// Make request - should get job ID back
	req := httptest.NewRequest("POST", "/api/v1/build?kernel_version="+kernelVersion, &headersArchive)
	req.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status 202, got %d", w.Code)
	}

	var resp model.BuildResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Status != model.StatusBuilding {
		t.Errorf("Expected status 'building', got %v", resp.Status)
	}

	if resp.JobID != cacheKey {
		t.Errorf("Expected job ID %s, got %s", cacheKey, resp.JobID)
	}
}

func TestServer_ConcurrentJobs(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer(&cacheDir)
	s.registerRoutes()

	// Create multiple jobsRepo concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			jobKey := "job-" + string(rune('0'+id))
			job := &service.BuildJob{
				JobInfo: service.JobInfo{
					Key:           jobKey,
					KernelVersion: "5.15.0-86-generic",
					Status:        model.StatusPending,
					CreatedAt:     time.Now(),
				},
			}
			if err := s.buildService.AddJob(job); err != nil {
				t.Errorf("Job failed with error: %v", err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	if s.buildService.JobsCount() != 10 {
		t.Errorf("Expected 10 jobsRepo, got %d", s.buildService.JobsCount())
	}
}
