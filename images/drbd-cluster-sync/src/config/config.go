package config

import (
	"os"
	"strconv"
)

const (
	RETRY_COUNT     = 20
	RETRY_DELAY_SEC = 2
	NUM_WORKERS     = 3
)

type Options struct {
	RetryCount    uint
	RetryDelaySec uint
	NumWorkers    int
}

func NewDefaultOptions() *Options {
	var opts Options

	retryCount := os.Getenv("RETRY_COUNT")
	retryDelay := os.Getenv("RETRY_DELAY")
	numWorkers := os.Getenv("NUM_WORKERS")

	if count, err := strconv.Atoi(retryCount); err == nil {
		opts.RetryCount = uint(count)
	}

	if delay, err := strconv.Atoi(retryDelay); err == nil {
		opts.RetryDelaySec = uint(delay)
	}

	if num, err := strconv.Atoi(numWorkers); err == nil {
		opts.NumWorkers = num
	}

	return &opts
}
