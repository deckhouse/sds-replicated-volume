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
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"storage-network-controller/internal/config"
	"storage-network-controller/internal/logger"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type discoveredIPs []string

func DiscoveryLoop(cfg config.Options, mgr manager.Manager, log *logger.Logger) (error) {
	storageNetworks, err := parseCIDRs(cfg.StorageNetworkCIDR)
	if err != nil {
		log.Error(err, "Cannot parse storage-network-cidr")
		return err
	}

	cl := mgr.GetClient()

	// initialize in-memory node IPs cache
	discoveredIPs := make(discoveredIPs, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// listen for interrupts or the Linux SIGTERM signal and cancel
	// our context, which the leader election code will observe and
	// step down
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		log.Info("Received termination, signaling shutdown")
		cancel()
	}()

	// discoverer loop
	for {
		if err := discovery(storageNetworks, &discoveredIPs, ctx, &cl, log); err != nil {
			log.Error(err, "Discovery error occured")
			cancel()
			return err
		}

		time.Sleep(time.Duration(cfg.DiscoverySec) * time.Second)
	}
}

func parseCIDRs(cidrs config.StorageNetworkCIDR) ([]netip.Prefix, error) {
	networks := make([]netip.Prefix, len(cidrs))

	var err error

	for i, cidr := range cidrs {
		if networks[i], err = netip.ParsePrefix(cidr); err != nil {
			return nil, err
		}
	}

	return networks, nil
}

func discovery(storageNetworks []netip.Prefix, cachedIPs *discoveredIPs, ctx context.Context, cl *client.Client, log *logger.Logger) error {
	select {
	case <-ctx.Done():
		// do nothing in case of cancel
		return nil

	default:
		log.Trace(fmt.Sprintf("[discovery] storageNetworks: %s, cachedIPs: %s", storageNetworks, *cachedIPs))

		addrs, err := net.InterfaceAddrs()
		if err != nil {
			log.Error(err, "Cannot get network interfaces")
			return err
		}

		var foundedIP discoveredIPs

		for _, address := range addrs {
			// check the address type: it should be not a loopback and in a private range
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.IsPrivate() {
				if ipnet.IP.To4() != nil {
					ipStr := ipnet.IP.String()

					if IPInCIDR(storageNetworks, ipStr, log) {
						log.Debug(fmt.Sprintf("IP '%s' found in CIDR blocks", ipStr))
						foundedIP = append(foundedIP, ipStr)
					} else {
						log.Trace(fmt.Sprintf("IP '%s' not founded in any CIDR blocks", ipStr))
					}
				}
			}
		}

		// if we found any IP that match with storageNetwork CIDRs that not already in cache
		// TODO: theoretically, there is a possible case where the IP changes, but the length does not change
		if len(foundedIP) != len(*cachedIPs) {
			log.Info(fmt.Sprintf("Founded %d storage network IPs: %s", len(foundedIP), strings.Join(foundedIP, ", ")))
			*cachedIPs = foundedIP
			// TODO: get node name from env var NODE_NAME, use client to get node info (from cache) and update it status field
		}
	}

	return nil
}