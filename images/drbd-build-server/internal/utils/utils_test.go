package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCacheKey(t *testing.T) {
	kernelVersion := "5.15.0-86-generic"
	headersData1 := []byte("test headers data 1")
	headersData2 := []byte("test headers data 2")

	key1 := GenerateCacheKey(kernelVersion, headersData1)
	key2 := GenerateCacheKey(kernelVersion, headersData2)
	key3 := GenerateCacheKey(kernelVersion, headersData1)

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
	if err := ExtractTarGz(&buf, tmpDir); err != nil {
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
	err := ExtractTarGz(&buf, tmpDir)
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
	found, err := FindKOFiles(tmpDir)
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
	if err := CreateTarGz(&buf, files, tmpDir); err != nil {
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
