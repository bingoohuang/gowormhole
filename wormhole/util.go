package wormhole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/bingoohuang/gg/pkg/ss"
	"github.com/bingoohuang/gowormhole/internal/util"
	"golang.org/x/crypto/nacl/secretbox"
	"nhooyr.io/websocket"
)

func dialWebsocket(ctx context.Context, slot, sigserv, bearer string) (*websocket.Conn, error) {
	u, err := url.Parse(sigserv)
	if err != nil {
		return nil, err
	}
	u.Scheme = util.If(ss.AnyOf(u.Scheme, "http", "ws"), "ws", "wss")
	if slot != "" {
		u.Path += slot
	}
	wsaddr := u.String()

	// Start the handshake.
	d := &websocket.DialOptions{
		Subprotocols: []string{Protocol},
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + bearer}},
	}
	ws, _, err := websocket.Dial(ctx, wsaddr, d)
	return ws, err
}

func readEncJSON(ctx context.Context, ws *websocket.Conn, key *[32]byte, v interface{}) ([]byte, error) {
	encrypted, err := readBase64(ctx, ws)
	if err != nil {
		return nil, err
	}

	var nonce [24]byte
	copy(nonce[:], encrypted[:24])
	j, ok := secretbox.Open(nil, encrypted[24:], &nonce, key)
	if !ok {
		return nil, ErrBadKey
	}
	return j, json.Unmarshal(j, v)
}

func writeEncJSON(ctx context.Context, ws *websocket.Conn, key *[32]byte, v interface{}) ([]byte, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var nonce [24]byte
	util.RandFull(nonce[:])
	data := secretbox.Seal(nonce[:], j, &nonce, key)
	return j, writeBase64(ctx, ws, data)
}

func readBase64(ctx context.Context, ws *websocket.Conn) ([]byte, error) {
	_, buf, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	return base64.URLEncoding.DecodeString(string(buf))
}

func writeBase64(ctx context.Context, ws *websocket.Conn, p []byte) error {
	return ws.Write(ctx, websocket.MessageText, []byte(base64.URLEncoding.EncodeToString(p)))
}
