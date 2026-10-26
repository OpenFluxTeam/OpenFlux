// Package vkteams carries the tunnel over a VK Teams (Myteam / OK vchat) call.
// Both ends join the same call as guest participants and exchange the tunnel as
// RTP media (the Telemost technique: data in the media payload), striped over
// many TURN allocations. No end is an external relay target: each lane sends
// only to the OTHER participant of the call.
//
// This file is the join: the OK vchat guest chain, ported from the turnprobe
// measurement tool, that yields the call's media-signaling endpoint and ICE
// servers. Both ends run GuestJoin, so both are real guests of the call.
package vkteams

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	baseURL    = "https://u.myteam.vmailru.net"
	apiVersion = "v149"
	okAPIBase  = "https://calls.okcdn.ru"
	okAppKey   = "CIAPPDKGDIHBABABA"
	guestName  = "Гость"
	userAgent  = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// CallInfo is what a join yields: the videowebrtc signaling endpoint (and its
// WebTransport sibling) and the call's ICE servers.
type CallInfo struct {
	Endpoint     string // wss://videowebrtc.okcdn.ru/ws2?...   (media signaling bus)
	WTEndpoint   string // https://videowebrtc.okcdn.ru:23456/wt?...  (WebTransport)
	TURNURLs     []string
	TURNUser     string
	TURNCred     string
	STUNURLs     []string
	P2PForbidden bool
	Raw          map[string]any
}

var httpc = &http.Client{Timeout: 25 * time.Second}

func httpDo(method, u string, h map[string]string, body string) ([]byte, int, error) {
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

// msgHeaders are the headers the messenger's web client sends.
func msgHeaders(extra map[string]string) map[string]string {
	h := map[string]string{"User-Agent": userAgent, "Origin": "https://myteam.mail.ru", "Referer": "https://myteam.mail.ru/"}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

// okAPI calls the OK calls API (fb.do) the way its web SDK does: a form, an
// object in it as JSON, the session key when there is one.
func okAPI(method string, params map[string]any, sessionKey string) (map[string]any, error) {
	form := url.Values{}
	form.Set("method", method)
	form.Set("format", "JSON")
	form.Set("application_key", okAppKey)
	for k, v := range params {
		if m, ok := v.(map[string]any); ok {
			b, _ := json.Marshal(m)
			form.Set(k, string(b))
		} else {
			form.Set(k, fmt.Sprint(v))
		}
	}
	if sessionKey != "" {
		form.Set("session_key", sessionKey)
	}
	b, st, err := httpDo("POST", okAPIBase+"/fb.do",
		msgHeaders(map[string]string{"Content-Type": "application/x-www-form-urlencoded"}), form.Encode())
	if err != nil {
		return nil, err
	}
	var j map[string]any
	if err := json.Unmarshal(b, &j); err != nil || st != 200 || j["error_msg"] != nil || j["error_code"] != nil {
		return nil, fmt.Errorf("%s: status %d error %v", method, st, j["error_code"])
	}
	return j, nil
}

func dig(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

// randomID is an ordinary random id of a browser's shape (8-4-4-4-12 hex).
func randomID() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 36)
	for i := range b {
		b[i] = hex[rand.Intn(16)]
	}
	b[8], b[13], b[18], b[23] = '-', '-', '-', '-'
	b[14] = '4'
	return string(b)
}

// GuestJoin runs the OK vchat guest chain for a VK Teams call code (the part
// after /join/) and returns the call's signaling endpoint and ICE servers.
// Each tunnel end runs its own GuestJoin, so both are real guest participants.
func GuestJoin(code string) (*CallInfo, error) {
	ts := time.Now().Unix()
	devID := randomID()

	// 1. Auth as guest -> token + sn.
	body, _ := json.Marshal(map[string]any{
		"reqId":  fmt.Sprintf("78200-%d", ts),
		"params": map[string]any{"name": guestName, "scopes": []string{"calls/" + code}},
	})
	b, st, err := httpDo("POST", baseURL+"/api/"+apiVersion+"/rapi/auth/guest",
		msgHeaders(map[string]string{"Content-Type": "application/json"}), string(body))
	if err != nil || st != 200 {
		return nil, fmt.Errorf("guest auth: status %d %v", st, err)
	}
	var g map[string]any
	_ = json.Unmarshal(b, &g)
	token, _ := dig(g, "results", "token").(string)
	sn, _ := dig(g, "results", "sn").(string)
	if token == "" {
		return nil, fmt.Errorf("guest auth: no token")
	}

	// 2. Start WIM session -> aimsid.
	sess := fmt.Sprintf("ts=%d&a=%s&userSn=%s&k=ic1zmlWFTdkiTnkL&view=online&clientName=webVKTeams&language=en-US"+
		"&deviceId=%s&sessionTimeout=2592000"+
		"&assertCaps=094613584C7F11D18222444553540000,0946135C4C7F11D18222444553540000"+
		"&events=myInfo,presence,webrtcMsg",
		ts, url.QueryEscape(token), url.QueryEscape(sn), url.QueryEscape(devID))
	b, st, err = httpDo("POST", baseURL+"/api/"+apiVersion+"/wim/aim/startSession",
		msgHeaders(map[string]string{"Content-Type": "application/x-www-form-urlencoded"}), sess)
	if err != nil || st != 200 {
		return nil, fmt.Errorf("startSession: status %d %v", st, err)
	}
	var s map[string]any
	_ = json.Unmarshal(b, &s)
	aimsid, _ := dig(s, "response", "data", "aimsid").(string)
	if aimsid == "" {
		return nil, fmt.Errorf("startSession: no aimsid")
	}

	// 3. OK calls auth token.
	body, _ = json.Marshal(map[string]any{"reqId": fmt.Sprintf("10711-%d", ts), "aimsid": aimsid})
	b, st, err = httpDo("POST", baseURL+"/api/"+apiVersion+"/rapi/okcalls/getAuthToken",
		msgHeaders(map[string]string{"Content-Type": "application/json", "X-Teams-Aimsid": aimsid}), string(body))
	if err != nil || st != 200 {
		return nil, fmt.Errorf("getAuthToken: status %d %v", st, err)
	}
	var t map[string]any
	_ = json.Unmarshal(b, &t)
	callsToken, _ := dig(t, "results", "token").(string)
	if callsToken == "" {
		return nil, fmt.Errorf("getAuthToken: no token")
	}

	// 4. Anonymous login to the OK calls API -> session key.
	login, err := okAPI("auth.anonymLogin", map[string]any{
		"session_data": map[string]any{
			"version": 3, "device_id": devID, "client_version": 1.1,
			"client_type": "SDK_JS", "auth_token": callsToken,
		},
	}, "")
	if err != nil {
		return nil, err
	}
	sk, _ := login["session_key"].(string)
	if sk == "" {
		return nil, fmt.Errorf("anonymLogin: no session_key")
	}

	// 5. Join the conversation by its link -> endpoint + ICE servers.
	conv, err := okAPI("vchat.joinConversationByLink", map[string]any{
		"joinLink": code, "isVideo": false, "protocolVersion": 6,
		"capabilities": "253F", "anonymToken": callsToken,
	}, sk)
	if err != nil {
		return nil, err
	}

	ci := &CallInfo{Raw: conv}
	ci.Endpoint, _ = conv["endpoint"].(string)
	ci.WTEndpoint, _ = conv["wt_endpoint"].(string)
	ci.P2PForbidden, _ = conv["p2p_forbidden"].(bool)
	ci.TURNUser, _ = dig(conv, "turn_server", "username").(string)
	ci.TURNCred, _ = dig(conv, "turn_server", "credential").(string)
	ci.TURNURLs = strList(dig(conv, "turn_server", "urls"))
	ci.STUNURLs = strList(dig(conv, "stun_server", "urls"))
	if ci.Endpoint == "" || len(ci.TURNURLs) == 0 {
		return nil, fmt.Errorf("join: no endpoint/turn_server in the answer")
	}
	return ci, nil
}

func strList(v any) []string {
	switch u := v.(type) {
	case []any:
		var out []string
		for _, x := range u {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{u}
	}
	return nil
}
