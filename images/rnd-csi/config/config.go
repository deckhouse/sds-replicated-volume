/*
Copyright 2023 Flant JSC

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
	"fmt"
	"os"
	"strconv"
)

const (
	NodeName = "KUBE_NODE_NAME"
	LogLevel = "LOG_LEVEL"
)

type Options struct {
	NodeName string
	Version  string
	LogLevel uint32
}

func NewConfig() (*Options, error) {
	var opts Options

	opts.NodeName = os.Getenv(NodeName)
	if opts.NodeName == "" {
		return nil, fmt.Errorf("[NewConfig] required %s env variable is not specified", NodeName)
	}

	if os.Getenv(LogLevel) == "" {
		opts.LogLevel = 2
	} else {
		logLevel, err := strconv.ParseUint(os.Getenv(LogLevel), 10, 32)
		if err != nil {
			fmt.Println("error read LOG_LEVEL")
			return nil, err
		}
		opts.LogLevel = uint32(logLevel)
	}

	opts.Version = "dev"

	return &opts, nil
}
