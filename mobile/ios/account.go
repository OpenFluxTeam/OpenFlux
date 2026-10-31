//go:build ios

package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"strconv"

	mobile "openflux-mobile"
)

// OpenFluxOfferExitCookies hands a sign-in (a Cookie header, "a=1; b=2") to
// the exit over the running client Session, for each of its transports
// whose type is in transportTypes (comma-separated, "vyandex,yandex"): the
// app's «Передать вход ноде». Returns how many transports it went to as
// text ("2"), or the reason it could not ("нет подключения к ноде"). The
// caller frees it with OpenFluxFreeString.
//
//export OpenFluxOfferExitCookies
func OpenFluxOfferExitCookies(transportTypes, cookies *C.char) *C.char {
	if transportTypes == nil || cookies == nil {
		return C.CString("нет cookies")
	}
	n, err := mobile.OfferExitCookies(C.GoString(transportTypes), C.GoString(cookies))
	if err != nil {
		return C.CString(err.Error())
	}
	return C.CString(strconv.Itoa(n))
}
