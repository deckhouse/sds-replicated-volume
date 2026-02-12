package suite

import (
	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

type LVGByNodeName func(nodeName string) *snc.LVMVolumeGroup
