package wormhole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"

	"github.com/bingoohuang/gg/pkg/ss"
	"github.com/bingoohuang/gowormhole/internal/util"
	"golang.org/x/crypto/nacl/secretbox"
	"nhooyr.io/websocket"
)

func dialWebsocket(ctx context.Context, slot, sigserv string) (*websocket.Conn, error) {
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
	dialOptions := &websocket.DialOptions{Subprotocols: []string{Protocol}}
	ws, _, err := websocket.Dial(ctx, wsaddr, dialOptions)
	return ws, err
}

func readEncJSON(ctx context.Context, ws *websocket.Conn, key *[32]byte, v interface{}) error {
	encrypted, err := readBase64(ctx, ws)
	if err != nil {
		return err
	}

	var nonce [24]byte
	copy(nonce[:], encrypted[:24])
	jsonmsg, ok := secretbox.Open(nil, encrypted[24:], &nonce, key)
	if !ok {
		return ErrBadKey
	}
	return json.Unmarshal(jsonmsg, v)
}

func writeEncJSON(ctx context.Context, ws *websocket.Conn, key *[32]byte, v interface{}) error {
	j, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var nonce [24]byte
	util.RandFull(nonce[:])
	data := secretbox.Seal(nonce[:], j, &nonce, key)
	return writeBase64(ctx, ws, data)
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
