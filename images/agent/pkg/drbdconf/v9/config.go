// Missing sections:
//   - require-drbd-module-version-{eq,ne,gt,ge,lt,le}
//   - stacked-on-top-of
//
// Missing sections parameters:
//   - net.transport
package v9

type Config struct {
	Common    *Common
	Global    *Global
	Resources []*Resource
}
