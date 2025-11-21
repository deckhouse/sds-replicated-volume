package utils

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InitLogger initializes structured logger with the specified log level
func InitLogger(levelStr string) *slog.Logger {
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

// generateCacheKey creates a unique key for caching based on kernel version and headers hash
func GenerateCacheKey(kernelVersion string, headersData []byte) string {
	h := sha256.New()
	h.Write([]byte(kernelVersion))
	h.Write(headersData)
	return hex.EncodeToString(h.Sum(nil))
}

// copyDirectory recursively copies a directory from src to dst.
// Uses cp -a to preserve permissions, timestamps, and handle symlinks.
func CopyDirectory(src, dst, jobID string, logger *slog.Logger) error {
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

func ExtractTarGz(r io.Reader, destDir string) error {
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
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

func FindKOFiles(rootDir string) ([]string, error) {
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

func CreateTarGz(w io.Writer, files []string, baseDir string) error {
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
