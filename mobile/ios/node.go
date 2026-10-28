//go:build ios

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"

	mobile "openflux-mobile"
)

// The node wizard for the iOS app: the same Node* calls the Android bridge
// makes (SSH to the person's VDS, the pinned node-install.sh, the channel's
// documents and rooms), each answering JSON - {"ok":true,...} or
// {"ok":false,"error":...} with the flags the wizard reads (hostKey, trust,
// mismatch, sudo, captcha). The calls block; the app makes them off its main
// thread, one at a time. Free every result with OpenFluxFreeString.

func goBool(b C.int) bool { return b != 0 }

func linkResult(link string, err error) *C.char {
	fields := map[string]interface{}{"ok": err == nil}
	if err != nil {
		fields["error"] = err.Error()
	} else {
		fields["link"] = link
	}
	b, _ := json.Marshal(fields)
	return C.CString(string(b))
}

//export OpenFluxNodeConnect
func OpenFluxNodeConnect(host *C.char, port C.int, user, password, privateKey, passphrase, hostKey, source *C.char) *C.char {
	return C.CString(mobile.NodeConnect(C.GoString(host), int(port), C.GoString(user), C.GoString(password),
		C.GoString(privateKey), C.GoString(passphrase), C.GoString(hostKey), C.GoString(source)))
}

//export OpenFluxNodeDisconnect
func OpenFluxNodeDisconnect() { mobile.NodeDisconnect() }

//export OpenFluxNodeNewChannel
func OpenFluxNodeNewChannel() *C.char { return C.CString(mobile.NodeNewChannel()) }

//export OpenFluxNodePlan
func OpenFluxNodePlan(channel *C.char, port C.int, transportsJSON *C.char, withCookies, autoUpdate C.int, mode *C.char) *C.char {
	return C.CString(mobile.NodePlan(C.GoString(channel), int(port), C.GoString(transportsJSON),
		goBool(withCookies), goBool(autoUpdate), C.GoString(mode)))
}

//export OpenFluxNodeApply
func OpenFluxNodeApply(channel, transportsJSON, key *C.char, port C.int, autoUpdate C.int, sudoPassword, cookieHeader, mode *C.char) *C.char {
	return C.CString(mobile.NodeApply(C.GoString(channel), C.GoString(transportsJSON), C.GoString(key), int(port),
		goBool(autoUpdate), C.GoString(sudoPassword), C.GoString(cookieHeader), C.GoString(mode)))
}

//export OpenFluxNodeShowChannel
func OpenFluxNodeShowChannel(channel, sudoPassword *C.char) *C.char {
	return C.CString(mobile.NodeShowChannel(C.GoString(channel), C.GoString(sudoPassword)))
}

//export OpenFluxNodeSetTransports
func OpenFluxNodeSetTransports(channel, transportsJSON, cookieHeader, sudoPassword, mode *C.char) *C.char {
	return C.CString(mobile.NodeSetTransports(C.GoString(channel), C.GoString(transportsJSON), C.GoString(cookieHeader),
		C.GoString(sudoPassword), C.GoString(mode)))
}

//export OpenFluxNodePushSignIn
func OpenFluxNodePushSignIn(channel, transportsJSON, cookieHeader, sudoPassword *C.char) *C.char {
	return C.CString(mobile.NodePushSignIn(C.GoString(channel), C.GoString(transportsJSON), C.GoString(cookieHeader),
		C.GoString(sudoPassword)))
}

//export OpenFluxNodeRemove
func OpenFluxNodeRemove(channel, sudoPassword *C.char) *C.char {
	return C.CString(mobile.NodeRemove(C.GoString(channel), C.GoString(sudoPassword)))
}

//export OpenFluxNodeUpgrade
func OpenFluxNodeUpgrade(sudoPassword *C.char) *C.char {
	return C.CString(mobile.NodeUpgrade(C.GoString(sudoPassword)))
}

//export OpenFluxNodeCheckDocument
func OpenFluxNodeCheckDocument(documentURL *C.char) *C.char {
	return C.CString(mobile.NodeCheckDocument(C.GoString(documentURL)))
}

//export OpenFluxNodeCreateCupsRooms
func OpenFluxNodeCreateCupsRooms() *C.char { return C.CString(mobile.NodeCreateCupsRooms()) }

//export OpenFluxNodeCreateAvitoRooms
func OpenFluxNodeCreateAvitoRooms() *C.char { return C.CString(mobile.NodeCreateAvitoRooms()) }

// OpenFluxNodeShareLink answers {"ok":true,"link":...} or {"ok":false,"error":...}.
//
//export OpenFluxNodeShareLink
func OpenFluxNodeShareLink(name, transportsJSON, key, host *C.char, port C.int) *C.char {
	return linkResult(mobile.NodeShareLink(C.GoString(name), C.GoString(transportsJSON), C.GoString(key), C.GoString(host), int(port)))
}

// OpenFluxNodeShareLinkContext is OpenFluxNodeShareLink with the channel's KDF context.
//
//export OpenFluxNodeShareLinkContext
func OpenFluxNodeShareLinkContext(name, transportsJSON, key, host *C.char, port C.int, context *C.char) *C.char {
	return linkResult(mobile.NodeShareLinkContext(C.GoString(name), C.GoString(transportsJSON), C.GoString(key),
		C.GoString(host), int(port), C.GoString(context)))
}
