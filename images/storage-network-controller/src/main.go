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

package main

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	goruntime "runtime"

	"storage-network-controller/internal/config"
	"storage-network-controller/internal/logger"
)

func IPInCIDR(networks []netip.Prefix, ip string, log *logger.Logger) bool {
	netIP, err := netip.ParseAddr(ip)

	if err != nil {
		log.Error(err, fmt.Sprintf("IPInCIDR: cannot parse IP %s", ip))
		return false
	}

	for _, network := range networks {
		if network.Contains(netIP) {
			return true
		}
	}

	return false
}

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		fmt.Println("unable to create NewConfig " + err.Error())
	}

	log, err := logger.NewLogger(cfg.Loglevel)
	if err != nil {
		fmt.Printf("unable to create NewLogger, err: %v\n", err)
		os.Exit(1)
	}

	log.Info(fmt.Sprintf("Go Version:%s ", goruntime.Version()))
	log.Info(fmt.Sprintf("OS/Arch:Go OS/Arch:%s/%s ", goruntime.GOOS, goruntime.GOARCH))

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Error(err, "Cannot get network interfaces")
		os.Exit(1)
	}

	networks := make([]netip.Prefix, len(cfg.StorageNetworkCIDR))

	for i, cidr := range cfg.StorageNetworkCIDR {
		networks[i], err = netip.ParsePrefix(cidr)

		if err != nil {
			log.Error(err, fmt.Sprintf("Cannot ParsePrefix for CIDR %s", cidr))
			os.Exit(2)
		}
	}

	for _, address := range addrs {
		// check the address type: it should be not a loopback and in a private range
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.IsPrivate() {
			if ipnet.IP.To4() != nil {
				ipStr := ipnet.IP.String()

				if IPInCIDR(networks, ipStr, log) {
					log.Debug(fmt.Sprintf("IP '%s' is in CIDR blocks", ipStr))
				} else {
					log.Debug(fmt.Sprintf("IP '%s' not founded in any CIDR blocks", ipStr))
				}
			}
		}
	}
}
