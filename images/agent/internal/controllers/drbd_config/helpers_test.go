package drbdconfig

import (
	"log/slog"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	v9 "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdconf/v9"
)

// ExposeKernelHasCrypto exposes kernelHasCrypto for black-box tests.
func ExposeKernelHasCrypto(name string) (bool, error) {
	return kernelHasCrypto(name)
}

// ExposeRvrOnThisNode wraps rvrOnThisNode for black-box tests.
func ExposeRvrOnThisNode(r *Reconciler, rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	return r.rvrOnThisNode(rvr)
}

// ExposeSharedSecretAlgUpdated wraps sharedSecretAlgUpdated for tests.
func ExposeSharedSecretAlgUpdated(
	r *Reconciler,
	rv *v1alpha3.ReplicatedVolume,
	rvr *v1alpha3.ReplicatedVolumeReplica,
	old *v1alpha3.ReplicatedVolumeReplica,
) bool {
	return r.sharedSecretAlgUpdated(rv, rvr, old)
}

// ExposeRvrInitialized wraps rvrInitialized for tests.
func ExposeRvrInitialized(r *Reconciler, rvr *v1alpha3.ReplicatedVolumeReplica, rv *v1alpha3.ReplicatedVolume) bool {
	return r.rvrInitialized(rvr, rv)
}

// ExposeAPIAddress converts address helper for tests.
func ExposeAPIAddress(node string, address v1alpha3.Address) v9.HostAddress {
	return apiAddressToV9HostAddress(node, address)
}

// ExposeTrimLen wraps trimLen for tests.
func ExposeTrimLen(s string, maxLen int) string {
	return trimLen(s, maxLen)
}

// NewUpHandlerForTests builds UpHandler with provided objects.
func NewUpHandlerForTests(
	rvr *v1alpha3.ReplicatedVolumeReplica,
	rv *v1alpha3.ReplicatedVolume,
	lvg *snc.LVMVolumeGroup,
	llv *snc.LVMLogicalVolume,
	nodeName string,
) *UpHandler {
	return &UpHandler{
		rvr:      rvr,
		rv:       rv,
		lvg:      lvg,
		llv:      llv,
		nodeName: nodeName,
		log:      slog.Default(),
	}
}

// ExposeGenerateResourceConfig helps covering generateResourceConfig.
func ExposeGenerateResourceConfig(h *UpHandler) *v9.Resource {
	return h.generateResourceConfig()
}

// ExposePopulateResourceForNode covers populateResourceForNode helper.
func ExposePopulateResourceForNode(
	h *UpHandler,
	res *v9.Resource,
	nodeName string,
	nodeID uint,
	peer *v1alpha3.Peer,
) {
	h.populateResourceForNode(res, nodeName, nodeID, peer)
}

// ExposeFileSystemError converts to DRBD errors.
func ExposeFileSystemError(err error) *v1alpha3.MessageError {
	apiErrs := &v1alpha3.DRBDErrors{}
	fileSystemOperationError{error: err}.ToDRBDErrors(apiErrs)
	return apiErrs.FileSystemOperationError
}

// ExposeCommandError converts command error to DRBD CmdError.
func ExposeCommandError(cmdErr drbdadm.CommandError) *v1alpha3.CmdError {
	apiErrs := &v1alpha3.DRBDErrors{}
	configurationCommandError{CommandError: cmdErr}.ToDRBDErrors(apiErrs)
	return apiErrs.ConfigurationCommandError
}

type unknownTestRequest struct{}

func (unknownTestRequest) _isRequest() {}

// NewUnknownRequestForTests helps cover default request handling.
func NewUnknownRequestForTests() Request {
	return unknownTestRequest{}
}

type unknownRVRRequest struct{ name string }

func (unknownRVRRequest) _isRequest() {}
func (u unknownRVRRequest) RVRName() string {
	return u.name
}

// NewUnknownRVRRequestForTests helps cover default RVR request branch.
func NewUnknownRVRRequestForTests(name string) RVRRequest {
	return unknownRVRRequest{name: name}
}

// CallIsRequest helpers trigger coverage of marker methods.
func CallIsRequestMarkers() {
	UpRequest{}._isRequest()
	DownRequest{}._isRequest()
	NewSharedSecretAlgRequest("", "")._isRequest()
}
