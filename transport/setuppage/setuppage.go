// Package setuppage holds the rules about the pages a transport asks an app
// to show (a sign-in, a check, a script's own setup page) that need no script
// engine: which page is a script's own, which check a remote exit may ask
// for, and how a page's submission is flattened.
//
// They live apart from transport/script so that a build without the JS engine
// (the iOS library: an App Store app carries no interpreter for code it could
// be handed later) can still show the native transports' sign-in and check
// pages. transport/script re-exports them under the same names.
package setuppage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// IsOwnPage reports whether a page a script raised is the script's own (an
// inline html page, or an http address on loopback, where only its own
// httpserver.listen() can answer: the script engine lets nothing else through)
// rather than a real site. The apps show an own page as it is, give it
// window.openfluxSubmit and do not collect cookies from it.
func IsOwnPage(addr, page string) bool {
	if page != "" {
		return true
	}
	u, err := url.Parse(addr)
	return err == nil && strings.EqualFold(u.Scheme, "http") && IsLoopbackHost(u.Hostname())
}

// IsLoopbackHost: localhost, 127.0.0.0/8 or ::1.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AcceptRemoteCheck reports whether a check a remote exit asks the client to
// show may be shown. An exit's inline page is self-contained and fine. An
// address must be a site (http or https) that is not on loopback: it is
// opened through the exit's proxy, and a loopback address from an exit is
// either meaningless here or, if the app opened it directly, would reach
// whatever listens on this machine on that port.
func AcceptRemoteCheck(addr, page string) bool {
	if page != "" {
		return true
	}
	u, err := url.Parse(addr)
	if err != nil {
		return false
	}
	if s := strings.ToLower(u.Scheme); s != "https" && s != "http" {
		return false
	}
	host := u.Hostname()
	if host == "" || IsLoopbackHost(host) {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return false
	}
	return true
}

// FlattenSubmission turns what a setup page handed to window.openfluxSubmit
// into the {name: value} map that reaches the script (ApplyCookies, then
// cookieJar.get() in onEvent("cookiesApplied")).
//
// The page may pass a flat object, or {client: {...}, node: {...}} (only the
// client half is delivered today). Values arrive as strings: a string is kept,
// a number or true/false becomes its text, null, objects and arrays are
// dropped (a page that needs structure sends JSON.stringify of it). An empty
// result is an error: a page that submits nothing has not set anything up.
func FlattenSubmission(raw []byte) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root map[string]interface{}
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("the page's data is not a JSON object: %w", err)
	}
	if client, ok := root["client"].(map[string]interface{}); ok {
		root = client
	}
	out := make(map[string]string, len(root))
	for k, v := range root {
		if k == "" {
			continue
		}
		switch x := v.(type) {
		case string:
			out[k] = x
		case json.Number:
			out[k] = x.String()
		case bool:
			out[k] = strconv.FormatBool(x)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the page passed no data")
	}
	return out, nil
}
