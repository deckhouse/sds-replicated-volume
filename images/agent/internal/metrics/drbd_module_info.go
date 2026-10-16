/*
Copyright 2026 Flant JSC

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

package metrics

import (
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// drbdModuleVersionPath is where the loaded DRBD kernel module reports its full version, including
// the build suffix (e.g. "9.2.18-flant.10"). drbd-reactor's drbd_version is built from the numeric
// version code and drops the suffix, so builds with the same upstream version cannot be told apart
// by it. The agent mounts the host /sys at /sys.
const drbdModuleVersionPath = "/sys/module/drbd/version"

// drbdModuleInfoCollector exports the loaded DRBD module version as an info metric. The file is
// read on every scrape: the module can be reloaded at any time, and the read is a single small file.
type drbdModuleInfoCollector struct {
	path string
	desc *prometheus.Desc
}

func newDRBDModuleInfoCollector(path string) *drbdModuleInfoCollector {
	return &drbdModuleInfoCollector{
		path: path,
		desc: prometheus.NewDesc(
			"sds_drbd_module_info",
			"Version of the DRBD kernel module loaded on the node, including the build suffix (from /sys/module/drbd/version); the value is always 1. Absent while the module is not loaded or the version cannot be read.",
			[]string{"version"},
			nil,
		),
	}
}

func (c *drbdModuleInfoCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *drbdModuleInfoCollector) Collect(ch chan<- prometheus.Metric) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	version := strings.TrimSpace(string(data))
	if version == "" {
		return
	}
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, 1, version)
}
