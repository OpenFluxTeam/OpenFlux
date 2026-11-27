//go:build ios

package registry

import (
	"errors"

	"github.com/p1neappleXpress/OpenFlux/transport"
)

// The iOS library is built without the JS engine: an App Store app must not
// carry an interpreter for code it could be handed later (App Store Review
// Guideline 2.5.2), even one nothing in the app calls. transport/script and
// goja are therefore not linked on iOS; the native transports are all there is.

// newScript refuses a script transport: this build has no engine to run it.
func newScript(url string, params map[string]interface{}, o Options) (transport.Transport, error) {
	return nil, errors.New("script: транспорты на JS в этой сборке не поддерживаются")
}

// fromScript never swaps a native transport for its JS package here.
func fromScript(typ, url string, params map[string]interface{}, o Options) (transport.Transport, bool) {
	return nil, false
}
