package artifact

// RegisterDefaults wires the stock renderers into r. Callers are expected
// to subsequently override entries (e.g. by registering an HTMLRenderer
// whose Template is populated for their schema) before serving traffic.
func RegisterDefaults(r *Registry) { r.Register(HTMLRenderer{}) }
