// Missing resources:
//   - require-drbd-module-version-{eq,ne,gt,ge,lt,le}
//   - stacked-on-top-of
//
// Missing resource parameters:
//   - net.transport
package v9

type Config struct {
	Common    *Common
	Global    *Global
	Resources []*Resource
}
