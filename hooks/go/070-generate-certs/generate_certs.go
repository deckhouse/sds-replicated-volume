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
		Namespace:     consts.ModuleNamespace,
		TLSSecretName: "spaas-certs-2",
		SANs: tlscertificate.DefaultSANs([]string{
			"spaas",
			fmt.Sprintf("spaas.%s", consts.ModuleNamespace),
			fmt.Sprintf("spaas.%s.svc", consts.ModuleNamespace),
		}),
		FullValuesPathPrefix: fmt.Sprintf("%s.internal.spaasCert2", consts.ModuleName),
	},
)
