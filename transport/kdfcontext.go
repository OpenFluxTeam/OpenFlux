package transport

// ContextPlaceholder is the --url default and, now, the single fixed
// encryption KDF context every peer uses unless a context was set explicitly.
const ContextPlaceholder = "http://#"

// ContextSource is one carrier as far as the encryption context cares. The
// fields are still filled by the call sites; KDFContexts no longer derives the
// context from them (see below), but the type stays the shape those callers
// pass so they need not change.
type ContextSource struct {
	Type     string
	URL      string
	Priority int
}

// KDFContexts returns the context a peer derives its encryption keys from (the
// scrypt salt), and the alternates it will also try.
//
// It used to compute the context from the carrier URLs and transport names, so
// the two peers had to independently arrive at the *same* string. In practice
// they diverged — build differences, URL normalisation, cupsonline room lists
// the exit can't know in advance — and when they did, the handshake failed. A
// list of candidate contexts ("alternates") was tried in turn to paper over
// it; that cycling is slow, fragile, and the thing that kept breaking.
//
// It is gone. The context is only a *public* salt — "secrecy comes exclusively
// from the secret" (see EncryptedTransport) — and per-session key uniqueness
// now comes from the handshake challenges folded into every key (see epoch.go
// and sessionSalt). So the context no longer has to encode anything about the
// channel:
//
//   - an explicit context (set with --session-context, or carried by an
//     openflux:// link) is shared out of band, so both peers hold the exact
//     same string and it cannot diverge — it is honoured as given;
//   - otherwise every peer, every build, every carrier uses the one fixed
//     ContextPlaceholder.
//
// Either way there are NO alternates and no context cycling: two peers that
// share the secret derive the same keys on the first try. globalURL and specs
// are ignored, kept only so the many call sites compile unchanged.
//
// Trade-off: a peer from an older build that still derives a URL-based context
// will not interoperate with this one (the keys differ), so this change must
// be rolled out to the whole fleet together. That is the deliberate cost of
// making divergence impossible rather than endlessly worked around.
func KDFContexts(explicit, globalURL string, specs []ContextSource) (primary string, alternates []string) {
	if explicit != "" {
		return explicit, nil
	}
	return ContextPlaceholder, nil
}
