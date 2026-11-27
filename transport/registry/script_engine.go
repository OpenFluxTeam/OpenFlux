//go:build !ios

package registry

import (
	"errors"
	"fmt"

	"github.com/p1neappleXpress/OpenFlux/transport"
	"github.com/p1neappleXpress/OpenFlux/transport/script"
)

// newScript builds a JS (goja) script transport. params carries the on-disk
// script ("path" to <name>.flux or <name>.js, its signature verified against
// the pinned "pubkey") plus whatever the script's own info().params asked the
// operator for. script.New verifies the signature before the script is ever
// evaluated, so a swapped file on disk fails closed here, not at connect. The
// core adds the role it knows and the script cannot (see withRole).
func newScript(url string, params map[string]interface{}, o Options) (transport.Transport, error) {
	str := func(key string) string {
		v, _ := params[key].(string)
		return v
	}
	path := str("path")
	if path == "" {
		return nil, errors.New("script: не указан путь к скрипту")
	}
	pub, err := script.DecodePublicKeyHex(str("pubkey"))
	if err != nil {
		return nil, fmt.Errorf("script: ключ автора: %w", err)
	}
	name := str("name")
	if name == "" {
		name = "script"
	}
	base := o.Base
	if o.LowMemory {
		base.MaxQueueSize = 512
	}
	return script.New(name, path, pub, url, withRole(withSettings(params), o.IsExit), base)
}
