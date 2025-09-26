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

package config

import "flag"

type Options struct {
	RetryCount            uint
	RetryDelaySec         uint
	NumWorkers            int
	LogLevel              string
	Burst                 int
	RPS                   float64
	LSEndpoint            string
	LSSkipTLSVerification bool
	BearerTokenFile       string
}

func NewDefaultOptions() *Options {
	var opts Options

	flag.UintVar(&opts.RetryCount, "retry-count", 10, "Number of retry attempts")
	flag.UintVar(&opts.RetryDelaySec, "retry-delay", 2, "Delay between retries in seconds")
	if opts.RetryDelaySec < 1 {
		opts.RetryDelaySec = 2 // set default value
	}

	flag.IntVar(&opts.NumWorkers, "num-workers", 3, "Number of workers")
	if opts.NumWorkers < 1 {
		opts.NumWorkers = 3 // set default value
	}

	flag.StringVar(&opts.LogLevel, "log-level", "info", "Enable debug log output. Choose from: panic, fatal, error, warn, info, debug")
	flag.IntVar(&opts.Burst, "linstor-api-burst", 1, "Maximum number of API requests allowed before being limited by requests-per-second. Default: 1 (no bursting)")
	flag.Float64Var(&opts.RPS, "linstor-api-requests-per-second", 0, "Maximum allowed number of LINSTOR API requests per second. Default: Unlimited")
	flag.StringVar(&opts.LSEndpoint, "linstor-endpoint", "", "Controller API endpoint for LINSTOR")
	flag.BoolVar(&opts.LSSkipTLSVerification, "linstor-skip-tls-verification", false, "If true, do not verify TLS")
	flag.StringVar(&opts.BearerTokenFile, "bearer-token", "", "Read the bearer token from the given file and use it for authentication.")

	flag.Parse()

	return &opts
}
