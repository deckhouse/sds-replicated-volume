package drbdconfig

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type SharedSecretAlgHandler struct {
	cl              client.Client
	rdr             client.Reader
	log             *slog.Logger
	rvName          string
	nodeName        string
	sharedSecretAlg string
}

func (h *SharedSecretAlgHandler) Handle(ctx context.Context) error {
	var rvr *v1alpha3.ReplicatedVolumeReplica
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := h.rdr.List(ctx, rvrList); err != nil {
		return fmt.Errorf("listing rvrs: %w", err)
	}

	for i := range rvrList.Items {
		if rvrList.Items[i].Spec.NodeName == h.nodeName && rvrList.Items[i].Spec.ReplicatedVolumeName == h.rvName {
			rvr = &rvrList.Items[i]
			break
		}
	}

	if rvr == nil {
		h.log.Debug("rvr not found for 'rv' on 'node'", "rv", h.rvName, "node", h.nodeName)
		return nil
	}

	if ok, err := kernelHasCrypto(h.sharedSecretAlg); !ok {
		if err != nil {
			h.log.Error("error during checking shared secret alg", "err", err)
		}

		patch := client.MergeFrom(rvr.DeepCopy())

		rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError = &v1alpha3.SharedSecretUnsupportedAlgError{
			UnsupportedAlg: h.sharedSecretAlg,
		}

		if err := h.cl.Patch(ctx, rvr, patch); err != nil {
			return fmt.Errorf("patching rvr unsupportedAlg: %w", err)
		}
	}

	return nil
}

func kernelHasCrypto(name string) (bool, error) {
	f, err := afs.Open("/proc/crypto")
	if err != nil {
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "name") {
			// line is like: "name         : aes"
			fields := strings.SplitN(line, ":", 2)
			if len(fields) == 2 && strings.TrimSpace(fields[1]) == name {
				found = true
			}
		}
		// each algorithm entry is separated by a blank line
		if line == "" && found {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}
