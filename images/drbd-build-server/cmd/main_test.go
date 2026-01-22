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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// newTestServer creates a test server with default logger level from flagLogLevel
func newTestServer() *server {
	return &server{
		router:   mux.NewRouter(),
		logger:   initLogger(*flagLogLevel),
		jobs:     make(map[string]*BuildJob),
		cacheDir: "",
	}
}

func TestGenerateCacheKey(t *testing.T) {
	kernelVersion := "5.15.0-86-generic"
	drbdVersion := "9.2.12"
	headersData1 := []byte("test headers data 1")
	headersData2 := []byte("test headers data 2")

	key1 := generateCacheKey(kernelVersion, drbdVersion, headersData1)
	key2 := generateCacheKey(kernelVersion, drbdVersion, headersData2)
	key3 := generateCacheKey(kernelVersion, drbdVersion, headersData1)

	if len(key1) != 64 {
		t.Errorf("Expected cache key length 64, got %d", len(key1))
	}

	if key1 == key2 {
		t.Error("Different headers should produce different keys")
	}

	if key1 != key3 {
		t.Error("Same headers should produce same keys")
	}
}

func TestExtractTarGz(t *testing.T) {
	// Create a test tar.gz archive
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	// Add a test file
	content := []byte("test content")
	header := &tar.Header{
		Name:     "test-file.txt",
		Size:     int64(len(content)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("Failed to write tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("Failed to write tar content: %v", err)
	}

	// Add a directory
	dirHeader := &tar.Header{
		Name:     "test-dir",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}
	if err := tarWriter.WriteHeader(dirHeader); err != nil {
		t.Fatalf("Failed to write dir header: %v", err)
	}

	tarWriter.Close()
	gzWriter.Close()

	// Extract
	tmpDir := t.TempDir()
	if err := extractTarGz(&buf, tmpDir); err != nil {
		t.Fatalf("Failed to extract tar.gz: %v", err)
	}

	// Verify extracted file
	extractedFile := filepath.Join(tmpDir, "test-file.txt")
	data, err := os.ReadFile(extractedFile)
	if err != nil {
		t.Fatalf("Failed to read extracted file: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("Expected content %q, got %q", string(content), string(data))
	}

	// Verify directory
	extractedDir := filepath.Join(tmpDir, "test-dir")
	if info, err := os.Stat(extractedDir); err != nil {
		t.Fatalf("Failed to stat extracted directory: %v", err)
	} else if !info.IsDir() {
		t.Error("Extracted item should be a directory")
	}
}

func TestExtractTarGzSecurity(t *testing.T) {
	// Test directory traversal protection
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	// Try to escape with ../
	header := &tar.Header{
		Name:     "../../../etc/passwd",
		Size:     0,
		Mode:     0644,
		Typeflag: tar.TypeReg,
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("Failed to write tar header: %v", err)
	}
	tarWriter.Close()
	gzWriter.Close()

	tmpDir := t.TempDir()
	err := extractTarGz(&buf, tmpDir)
	if err == nil {
		t.Error("Expected error for directory traversal attempt, got nil")
	}
	if !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("Expected 'invalid path' error, got: %v", err)
	}
}

func TestFindKOFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure
	moduleDir := filepath.Join(tmpDir, "lib", "modules", "5.15.0-86-generic", "updates")
	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		t.Fatalf("Failed to create module directory: %v", err)
	}

	// Create some .ko files
	koFiles := []string{"drbd.ko", "drbd_transport_tcp.ko", "drbd_transport_rdma.ko"}
	for _, koFile := range koFiles {
		file := filepath.Join(moduleDir, koFile)
		if err := os.WriteFile(file, []byte("fake ko content"), 0644); err != nil {
			t.Fatalf("Failed to create .ko file: %v", err)
		}
	}

	// Create a non-.ko file
	if err := os.WriteFile(filepath.Join(moduleDir, "not-a-ko.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Find .ko files
	found, err := findKOFiles(tmpDir)
	if err != nil {
		t.Fatalf("findKOFiles failed: %v", err)
	}

	if len(found) != len(koFiles) {
		t.Errorf("Expected %d .ko files, got %d", len(koFiles), len(found))
	}

	for _, expected := range koFiles {
		foundFile := false
		for _, f := range found {
			if strings.HasSuffix(f, expected) {
				foundFile = true
				break
			}
		}
		if !foundFile {
			t.Errorf("Expected to find %s, but it wasn't in the results", expected)
		}
	}
}

func TestHelloEndpoint(t *testing.T) {
	s := newTestServer()
	s.routes()

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
	s := newTestServer()
	s.cacheDir = cacheDir
	s.maxBytesBody = 100 * 1024 * 1024
	s.routes()

	req := httptest.NewRequest("POST", "/api/v1/build", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestBuildModuleEndpoint_CacheHit(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer()
	s.cacheDir = cacheDir
	s.maxBytesBody = 100 * 1024 * 1024
	s.routes()

	kernelVersion := "5.15.0-86-generic"
	drbdVersion := "9.2.12"
	headersData := []byte("test headers")

	// Create a minimal tar.gz archive for headers FIRST
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
	tarWriter.Close()
	gzWriter.Close()

	// Now generate cache key from the actual archive bytes
	archiveBytes := headersArchive.Bytes()
	cacheKey := generateCacheKey(kernelVersion, drbdVersion, archiveBytes)
	cachePath := filepath.Join(cacheDir, cacheKey+".tar.gz")

	// Create a cached file
	cachedContent := []byte("cached module content")
	if err := os.WriteFile(cachePath, cachedContent, 0644); err != nil {
		t.Fatalf("Failed to create cache file: %v", err)
	}

	// Reset archive buffer and recreate (since it was consumed)
	headersArchive.Reset()
	gzWriter = gzip.NewWriter(&headersArchive)
	tarWriter = tar.NewWriter(gzWriter)
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("Failed to write tar header: %v", err)
	}
	if _, err := tarWriter.Write(headersData); err != nil {
		t.Fatalf("Failed to write tar content: %v", err)
	}
	tarWriter.Close()
	gzWriter.Close()

	req := httptest.NewRequest("POST", "/api/v1/build?kernel_version="+kernelVersion+"&drbd_version="+drbdVersion, &headersArchive)
	req.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for cache hit, got %d", w.Code)
	}

	// Verify response contains cached content
	if !bytes.Equal(w.Body.Bytes(), cachedContent) {
		t.Error("Response body should contain cached content")
	}
}

func TestGetStatusEndpoint(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer()
	s.cacheDir = cacheDir
	s.routes()

	// Create a test job
	job := &BuildJob{
		Key:           "test-job-id",
		KernelVersion: "5.15.0-86-generic",
		Status:        StatusBuilding,
		CreatedAt:     time.Now(),
		CachePath:     filepath.Join(cacheDir, "test.tar.gz"),
	}
	s.jobs["test-job-id"] = job

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
	s := newTestServer()
	s.routes()

	req := httptest.NewRequest("GET", "/api/v1/status/non-existent-job", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestDownloadModuleEndpoint_NotCompleted(t *testing.T) {
	s := newTestServer()
	s.routes()

	job := &BuildJob{
		Key:           "test-job-id",
		KernelVersion: "5.15.0-86-generic",
		Status:        StatusBuilding,
		CreatedAt:     time.Now(),
	}
	s.jobs["test-job-id"] = job

	req := httptest.NewRequest("GET", "/api/v1/download/test-job-id", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestDownloadModuleEndpoint_Completed(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer()
	s.cacheDir = cacheDir
	s.routes()

	cachePath := filepath.Join(cacheDir, "test-modules.tar.gz")
	cachedContent := []byte("cached module tar.gz content")
	if err := os.WriteFile(cachePath, cachedContent, 0644); err != nil {
		t.Fatalf("Failed to create cache file: %v", err)
	}

	job := &BuildJob{
		Key:           "test-job-id",
		KernelVersion: "5.15.0-86-generic",
		Status:        StatusCompleted,
		CreatedAt:     time.Now(),
		CachePath:     cachePath,
	}
	s.jobs["test-job-id"] = job

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
	s := newTestServer()
	s.cacheDir = cacheDir
	s.maxBytesBody = 100 * 1024 * 1024
	s.routes()

	kernelVersion := "5.15.0-86-generic"
	drbdVersion := "9.2.12"
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
	tarWriter.Close()
	gzWriter.Close()

	// Generate cache key from actual archive bytes
	archiveBytes := headersArchive.Bytes()
	cacheKey := generateCacheKey(kernelVersion, drbdVersion, archiveBytes)

	// Create a job that's already building
	job := &BuildJob{
		Key:           cacheKey,
		KernelVersion: kernelVersion,
		DRBDVersion:   drbdVersion,
		Status:        StatusBuilding,
		CreatedAt:     time.Now(),
	}
	s.jobs[cacheKey] = job

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
	tarWriter.Close()
	gzWriter.Close()

	// Make request - should get job ID back
	req := httptest.NewRequest("POST", "/api/v1/build?kernel_version="+kernelVersion+"&drbd_version="+drbdVersion, &headersArchive)
	req.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("Expected status 202, got %d", w.Code)
	}

	var resp BuildResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Status != StatusBuilding {
		t.Errorf("Expected status 'building', got %v", resp.Status)
	}

	if resp.JobID != cacheKey {
		t.Errorf("Expected job ID %s, got %s", cacheKey, resp.JobID)
	}
}

func TestCreateTarGz(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	files := []string{"file1.ko", "file2.ko"}
	for _, file := range files {
		filePath := filepath.Join(tmpDir, file)
		if err := os.WriteFile(filePath, []byte("test content "+file), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Create tar.gz
	var buf bytes.Buffer
	if err := createTarGz(&buf, files, tmpDir); err != nil {
		t.Fatalf("Failed to create tar.gz: %v", err)
	}

	// Verify we can read it back
	gzReader, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	foundFiles := make(map[string]bool)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Failed to read tar entry: %v", err)
		}
		foundFiles[header.Name] = true
	}

	if len(foundFiles) != len(files) {
		t.Errorf("Expected %d files in archive, got %d", len(files), len(foundFiles))
	}

	for _, file := range files {
		if !foundFiles[file] {
			t.Errorf("File %s not found in archive", file)
		}
	}
}

func TestServer_ConcurrentJobs(t *testing.T) {
	cacheDir := t.TempDir()
	s := newTestServer()
	s.cacheDir = cacheDir
	s.routes()

	// Create multiple jobs concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			jobKey := "job-" + string(rune('0'+id))
			job := &BuildJob{
				Key:           jobKey,
				KernelVersion: "5.15.0-86-generic",
				Status:        StatusPending,
				CreatedAt:     time.Now(),
			}
			s.jmu.Lock()
			s.jobs[job.Key] = job
			s.jmu.Unlock()
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	s.jmu.RLock()
	if len(s.jobs) != 10 {
		t.Errorf("Expected 10 jobs, got %d", len(s.jobs))
	}
	s.jmu.RUnlock()
}
