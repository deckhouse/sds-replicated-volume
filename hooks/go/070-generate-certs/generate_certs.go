package generatecerts

import (
	"fmt"

	tlscertificate "github.com/deckhouse/module-sdk/common-hooks/tls-certificate"
	"github.com/deckhouse/sds-replicated-volume/hooks/go/consts"
)

// spaas
var _ = tlscertificate.RegisterInternalTLSHookEM(
	tlscertificate.GenSelfSignedTLSHookConf{
		CN:            "spaas",
		TLSSecretName: "spaas-certs",
		Namespace:     consts.ModuleNamespace,
		SANs: tlscertificate.DefaultSANs([]string{
			"spaas",
			fmt.Sprintf("spaas.%s", consts.ModuleNamespace),
			fmt.Sprintf("spaas.%s.svc", consts.ModuleNamespace),
		}),
		FullValuesPathPrefix: fmt.Sprintf("%s.internal.spaasCert", consts.ModuleName),
	},
)
