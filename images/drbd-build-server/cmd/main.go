package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/internal/utils"
	"github.com/deckhouse/sds-replicated-volume/images/drbd-build-server/pkg/control"
)

const (
	Version string = "0.0.0" // Version TODO: inject version later (19/11/2025)

	maxHeaderBytes int           = 4 * 1024
	readTimeoutMs  time.Duration = 30 * time.Minute
	writeTimeoutMs time.Duration = 30 * time.Minute
)

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

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Printf("Git-commit: '%s'\n", Version)
		os.Exit(0)
	}

	// Initialize log
	log := utils.InitLogger(*flagLogLevel)

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
		log.Error("Failed to create cache directory", "error", err)
		os.Exit(1)
	}

	// Check if base DRBD directory exists
	hasDRBD := false
	if info, err := os.Stat(*flagDRBDDir); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(*flagDRBDDir, "Makefile")); err == nil {
			hasDRBD = true
			log.Info("DRBD source found", "path", *flagDRBDDir, "note", "will be copied per build")
		}
	}

	// If no base DRBD directory and version specified, we'll clone per build
	if !hasDRBD && drbdVersion == "" {
		log.Warn("No DRBD source found and no version specified. Builds will fail.")
		log.Info("Either provide -drbd-dir with existing source or -drbd-version to clone.")
		return
	}

	requestHandler := control.NewBuildServer(
		flagDRBDDir, // Base directory (if exists, will be copied)
		flagCacheDir,
		flagMaxBytesBody,
		flagKeepTmpDir,
		&spaasURL,
		log,
		&drbdVersion,
		&drbdRepo,
		Version,
	)

	server := http.Server{
		Addr:           *flagAddr,
		Handler:        requestHandler,
		MaxHeaderBytes: maxHeaderBytes,
		ReadTimeout:    readTimeoutMs,
		WriteTimeout:   writeTimeoutMs,
	}

	if *flagCertFile != "" && *flagKeyFile != "" {
		log.Info("Starting TLS server", "addr", *flagAddr)
		if err := server.ListenAndServeTLS(*flagCertFile, *flagKeyFile); err != nil {
			log.Error("Server failed", "error", err)
		}
	} else {
		log.Info("Starting HTTP server", "addr", *flagAddr, "note", "TLS not configured")
		if err := server.ListenAndServe(); err != nil {
			log.Error("Server failed", "error", err)
		}
	}
}
