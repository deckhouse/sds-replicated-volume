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

package discoverer

import (
	"fmt"
	"net"
	"net/netip"

	"sigs.k8s.io/controller-runtime/pkg/manager"
	"storage-network-controller/internal/config"
	"storage-network-controller/internal/logger"
)

func Discovery(cfg config.Options, mgr manager.Manager, log *logger.Logger) (error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Error(err, "Cannot get network interfaces")
		return err
	}

	networks := make([]netip.Prefix, len(cfg.StorageNetworkCIDR))

	for i, cidr := range cfg.StorageNetworkCIDR {
		networks[i], err = netip.ParsePrefix(cidr)

		if err != nil {
			log.Error(err, fmt.Sprintf("Cannot ParsePrefix for CIDR %s", cidr))
			return err
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

	return nil
}
