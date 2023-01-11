package main

// This is the signalling server. It relays messages between peers wishing to connect.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/bingoohuang/gg/pkg/ss"
	"github.com/bingoohuang/godaemon"
	"github.com/bingoohuang/golog"
	"github.com/bingoohuang/gowormhole"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wormhole"
	"github.com/pion/webrtc/v3"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/acme/autocert"
	"nhooyr.io/websocket"
)

const (
	// slotTimeout is the maximum amount of time a client is allowed to hold a slot.
	slotTimeout = 12 * time.Hour

	importMeta = `<!doctype html>
<meta charset=utf-8>
<meta name="go-import" content="gowormhole.d5k.co git https://github.com/bingoohuang/gowormhole">
<meta http-equiv="refresh" content="0;URL='https://github.com/bingoohuang/gowormhole'">
`
	serviceWorkerPage = `You're not supposed to get this file or end up here.

This is a dummy URL is used by GoWormhole to help web browsers
efficiently download files from a WebRTC connection. It should be
handled entirely by a ServiceWorker running in your browser.

If you got this text instead of the file you expected to download,
it is possible your web browser doesn't fully support ServiceWorkers
but claims it does. Try a different web browser, and if that doesn't
work, please file a bug report.
`
)

var (
	turnUser    string
	turnServer  string
	stunServers []webrtc.ICEServer
)

// turnServers return the configured TURN server with HMAC-based ephemeral
// credentials generated as described in:
// https://tools.ietf.org/html/draft-uberti-behave-turn-rest-00
func turnServers() []webrtc.ICEServer {
	if turnServer == "" {
		return nil
	}

	username, credential := ss.Split2(turnUser, ss.WithSeps(":"))

	return []webrtc.ICEServer{{
		URLs:     []string{util.Prefix("turn:", util.AppendPort(turnServer, gowormhole.DefaultTurnPort))},
		Username: username, Credential: credential,
	}}
}

// relay sets up a rendezvous on a slot and pipes the two websockets together.
func relay(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// This sounds nasty but checking origin only matters if requests
		// change any user state on the server, aka CSRF. We don't have any
		// user state other than this ephemeral connection. So it's fine.
		InsecureSkipVerify: true,
		Subprotocols:       []string{wormhole.Protocol},
	})
	if err != nil {
		log.Println(err)
		return
	}

	if conn.Subprotocol() != wormhole.Protocol {
		// Make sure we negotiated the right protocol, since "blank" is also a default one.
		protocolErrorCounter.WithLabelValues("wrongversion").Inc()
		_ = conn.Close(wormhole.CloseWrongProto, "wrong protocol, please upgrade client")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), slotTimeout)
	initMsg := wormhole.InitMsg{ICEServers: append(turnServers(), stunServers...)}
	slotKey := r.URL.Path[1:] // strip leading slash

	var rconn atomic.Pointer[websocket.Conn]

	if rc, err := joinPeers(ctx, slotKey, conn, initMsg); err != nil {
		log.Printf("join peers failed: %v", err)
		if se, ok := err.(*slotError); ok {
			if se.CloseReason != "" {
				_ = conn.Close(se.CloseCode, se.CloseReason)
				slots.Delete(slotKey)
			}
		}
	} else {
		rconn.Store(rc)
	}

	defer cancel()
	for {
		msgType, p, err := conn.Read(ctx)
		if err != nil {
			log.Printf("read error: %v", err)
			switch websocket.CloseStatus(err) {
			case wormhole.CloseBadKey:
				iceCounter.WithLabelValues("fail", "badkey").Inc()
				closeConn(rconn.Load(), wormhole.CloseBadKey, "bad key")
			case wormhole.CloseWebRTCFailed:
				iceCounter.WithLabelValues("fail", "unknown").Inc()
			case wormhole.CloseWebRTCSuccess:
				iceCounter.WithLabelValues("success", "unknown").Inc()
			case wormhole.CloseWebRTCSuccessDirect:
				iceCounter.WithLabelValues("success", "direct").Inc()
			case wormhole.CloseWebRTCSuccessRelay:
				iceCounter.WithLabelValues("success", "relay").Inc()
			default:
				iceCounter.WithLabelValues("unknown", "unknown").Inc()
				closeConn(rconn.Load(), wormhole.ClosePeerHungUp, "peer hung up")
			}

			return
		}

		rc := rconn.Load()
		if rc == nil {
			// We could synchronise with the rendezvous goroutine above and wait for  B to connect,
			// but receiving anything at this stage is a protocol violation, so we should just bail out.
			return
		}
		if err := rc.Write(ctx, msgType, p); err != nil {
			log.Printf("write error: %v", err)
			return
		}
	}
}

func writeConn(ctx context.Context, c *websocket.Conn, initMsg wormhole.InitMsg) error {
	buf, err := json.Marshal(initMsg)
	if err != nil {
		return NewSlotError(initMsg.Slot, wormhole.CloseBadKey, "", err)
	}

	if err := c.Write(ctx, websocket.MessageText, buf); err != nil {
		return NewSlotError(initMsg.Slot, wormhole.CloseBadKey, "", err)
	}

	return nil
}

func closeConn(c *websocket.Conn, code websocket.StatusCode, reason string) {
	if c != nil {
		_ = c.Close(code, reason)
	}
}

func joinPeers(ctx context.Context, slotKey string, conn *websocket.Conn, initMsg wormhole.InitMsg) (*websocket.Conn, error) {
	slot, err := slots.Setup(slotKey)
	if err != nil {
		return nil, err
	}
	initMsg.Slot = slot.SlotKey
	initMsg.Mode = slot.Mode
	if err := writeConn(ctx, conn, initMsg); err != nil {
		return nil, err
	}

	log.Printf("slot: %s mode: %s", slot.SlotKey, slot.Mode)

	if slot.Mode == wormhole.ModePeer1 {
		// write current conn to slot.C
		if err := waitPair(ctx, conn, slot.C, slotKey); err != nil {
			return nil, err
		}

		return <-slot.C, nil
	}

	// Join an existing slot.
	var rconn *websocket.Conn
	select {
	case <-ctx.Done():
		return nil, NewSlotError(slotKey, wormhole.CloseSlotTimedOut, "timed out", nil)
	case rconn = <-slot.C: // 收到对端连接
	}

	slot.C <- conn
	rendezvousCounter.WithLabelValues("success").Inc()
	return rconn, nil
}

func waitPair(ctx context.Context, conn *websocket.Conn, sc chan *websocket.Conn, slotKey string) error {
	for {
		select {
		case <-ctx.Done():
			rendezvousCounter.WithLabelValues("timeout").Inc()
			return NewSlotError(slotKey, wormhole.CloseSlotTimedOut, "timed out", nil)
		case <-time.After(30 * time.Second): // Do a WebSocket Ping every 30 seconds.
			_ = conn.Ping(ctx)
		case sc <- conn:
			rendezvousCounter.WithLabelValues("success").Inc()
			return nil
		}
	}
}

func signallingServerCmd(ctx context.Context, args ...string) {
	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.Usage = func() {
		_, _ = fmt.Fprintf(f.Output(), "run the gowormhole signalling server\n\n")
		_, _ = fmt.Fprintf(f.Output(), "usage: %s %s\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(f.Output(), "flags:\n")
		f.PrintDefaults()
	}
	httpAddr := f.String("http", ":http", "http listen address")
	httpsAddr := f.String("https", "", "https listen address")
	debugAddr := f.String("debug", "", "debug and metrics listen address")
	hosts := f.String("hosts", "", "comma separated list of hosts by which site is accessible")
	bearer := f.String("bearer", "", "Bearer authentication in header, e.g. Authorization: Bearer xyz")
	secretPath := f.String("secrets", os.Getenv("HOME")+"/keys", "path to put let's encrypt cache")
	cert := f.String("cert", "", "https certificate (leave empty to use letsencrypt)")
	key := f.String("key", "", "https certificate key")
	pDaemon := f.Bool("daemon", false, "Daemonized")

	// mondain/public-stun-list.txt https://gist.github.com/mondain/b0ec1cf5f60ae726202e
	// https://github.com/pradt2/always-online-stun
	stun := f.String("stun", "stun2.l.google.com:19302", "list of STUN server addresses to tell clients to use")
	f.StringVar(&turnServer, "turn", "", "TURN server to use for relaying")
	f.StringVar(&turnUser, "turn-user", "", "turn user in TURN server, e.g. user:password")
	_ = f.Parse(args[1:])

	if (*cert == "") != (*key == "") {
		log.Fatalf("-cert and -key options must be provided together or both left empty")
	}
	if turnServer != "" && turnUser == "" {
		log.Fatal("cannot use a TURN server without a secret")
	}

	godaemon.Daemonize(*pDaemon)
	golog.Setup()

	for _, s := range strings.Split(*stun, ",") {
		if s != "" {
			stunServers = append(stunServers, webrtc.ICEServer{
				URLs: []string{util.Prefix("stun:", util.AppendPort(s, gowormhole.DefaultStunPort))},
			})
		}
	}

	fs := gziphandler.GzipHandler(http.FileServer(http.FS(gowormhole.Web)))

	handler := func(w http.ResponseWriter, r *http.Request) {
		if *bearer != "" && "Bearer "+*bearer != r.Header.Get("Authorization") {
			http.Error(w, "Not Authorized", http.StatusUnauthorized)
			return
		}

		if r.Header.Get("GoWormhole") == GowormholeReserveslotkey {
			reserveSlotKey(w)
			return
		}

		// Handle WebSocket connections.
		if strings.ToLower(r.Header.Get("Upgrade")) == "websocket" {
			relay(w, r)
			return
		}

		// Allow 3rd parties to load JS modules, etc.
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Disallow 3rd party code to run when we're the origin.
		// unsafe-eval is required for wasm :(
		// https://github.com/WebAssembly/content-security-policy/issues/7
		// connect-src is required for safari :(
		// https://bugs.webkit.org/show_bug.cgi?id=201591
		csp := "default-src 'self'; script-src 'self' 'unsafe-eval'; img-src 'self' blob:; connect-src 'self' ws://localhost/"
		for _, host := range strings.Split(*hosts, ",") {
			if host != "" {
				csp += fmt.Sprintf(" wss://%v", host)
				csp += fmt.Sprintf(" ws://%v", host)
			}
		}
		w.Header().Set("Content-Security-Policy", csp)
		// Set a small max age for cache. We might want to switch to a content-addressed
		// resource naming scheme and change this to immutable, but until then disable caching.
		w.Header().Set("Cache-Control", "no-cache")

		// Set HSTS header for 2 years on HTTPS connections.
		if *httpsAddr != "" {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000")
		}

		// Return a redirect to source code repo for the go get URL.
		if r.URL.Query().Get("go-get") == "1" || r.URL.Path == "/cmd/gowormhole" {
			_, _ = w.Write([]byte(importMeta))
			return
		}

		// Handle the Service Worker private prefix. A well-behaved Service Worker
		// must *never* reach us on this path.
		if strings.HasPrefix(r.URL.Path, "/_/") {
			protocolErrorCounter.WithLabelValues("serviceworkererr").Inc()
			http.Error(w, serviceWorkerPage, http.StatusNotFound)
			return
		}

		fs.ServeHTTP(w, r)
	}

	m := &autocert.Manager{
		Cache:      autocert.DirCache(*secretPath),
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(strings.Split(*hosts, ",")...),
	}

	srv := &http.Server{
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Minute,
		IdleTimeout:  20 * time.Second,
		Addr:         *httpAddr,
		Handler:      m.HTTPHandler(http.HandlerFunc(handler)),
	}

	errCh := make(chan error)
	if *debugAddr != "" {
		http.Handle("/metrics", promhttp.Handler())
		go func() { errCh <- http.ListenAndServe(*debugAddr, nil) }()
	}
	if *httpsAddr != "" {
		server := &http.Server{
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 60 * time.Minute,
			IdleTimeout:  20 * time.Second,
			Addr:         *httpsAddr,
			Handler:      http.HandlerFunc(handler),
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				CipherSuites: []uint16{
					tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
					tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
				},
			},
		}
		if *cert == "" && *key == "" {
			server.TLSConfig.GetCertificate = m.GetCertificate
		}
		srv.Handler = m.HTTPHandler(nil) // Enable redirect to https handler.
		go func() { errCh <- server.ListenAndServeTLS(*cert, *key) }()
	}
	if *httpAddr != "" {
		go func() { errCh <- srv.ListenAndServe() }()
	}
	log.Fatal(<-errCh)
}

type reserveSlotResult struct {
	Error string `json:"error"`
	Key   string `json:"key"`
}

func reserveSlotKey(w http.ResponseWriter) {
	item, err := slots.Reserve()

	var result reserveSlotResult
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Key = item.SlotKey
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(result)
	return
}
