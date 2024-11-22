/*
Copyright 2024 Flant JSC

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

import (
	"flag"
	"fmt"
	"net"
	"os"

	"sds-node-agent/internal/logger"
)

const (
	LogLevel = "LOG_LEVEL"
)

type StorageNetworkCIDR []string

type Options struct {
	StorageNetworkCIDR StorageNetworkCIDR

	Loglevel logger.Verbosity
}

// String is an implementation of the flag.Value interface
func (i *StorageNetworkCIDR) String() string {
	return fmt.Sprintf("%v", *i)
}

// Set is an implementation of the flag.Value interface
func (i *StorageNetworkCIDR) Set(value string) error {
	_, _, err := net.ParseCIDR(value)

	if err != nil {
		return err
	}

	*i = append(*i, value)
	return nil
}

func NewConfig() (*Options, error) {
	var opts Options

	loglevel := os.Getenv(LogLevel)
	if loglevel == "" {
		opts.Loglevel = logger.DebugLevel
	} else {
		opts.Loglevel = logger.Verbosity(loglevel)
	}

	fl := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fl.Var(&opts.StorageNetworkCIDR, "storage-network-cidr", "Set storage network CIDR blocks. Can be passed multiple times.")

	err := fl.Parse(os.Args[1:])
	if err != nil {
		fmt.Printf("error parsing flags, err: %s\n", err.Error())
		os.Exit(1)
	}

	return &opts, nil
}
