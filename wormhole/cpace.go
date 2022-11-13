package wormhole

import (
	"context"
	"crypto/sha256"
	"io"

	"filippo.io/cpace"
	"golang.org/x/crypto/hkdf"
	"nhooyr.io/websocket"
)

func exchangeKeySideA(ctx context.Context, ws *websocket.Conn, pass string) (key *[32]byte, err error) {
	// The identity arguments are to bind endpoint identities in PAKE. Cf. Unknown
	// Key-Share Attack. https://tools.ietf.org/html/draft-ietf-mmusic-sdp-uks-03
	//
	// In the context of a program like magic-wormhole we do not have ahead of time
	// information on the identity of the remote party. We only have the slot name,
	// and sometimes even that at this stage. But that's okay, since:
	//   a) The password is randomly generated and ephemeral.
	//   b) A peer only gets one guess.
	// An unintended destination is likely going to fail PAKE.

	msgA, pake, err := cpace.Start(pass, cpace.NewContextInfo("", "", nil))
	if err != nil {
		return nil, err
	}
	if err := writeBase64(ctx, ws, msgA); err != nil {
		return nil, err
	}
	logf("sent A pake msg (%v bytes)", len(msgA))

	msgB, err := readBase64(ctx, ws)
	if err != nil {
		if websocket.CloseStatus(err) == CloseWrongProto {
			err = ErrBadVersion
		}
		return nil, err
	}
	mk, err := pake.Finish(msgB)
	if err != nil {
		return nil, err
	}
	k := [32]byte{}
	if _, err := io.ReadFull(hkdf.New(sha256.New, mk, nil, nil), k[:]); err != nil {
		return nil, err
	}
	logf("have key, got B msg (%v bytes)", len(msgB))
	return &k, nil
}

func exhangeKeySideB(ctx context.Context, ws *websocket.Conn, pass string) (key *[32]byte, err error) {
	msgA, err := readBase64(ctx, ws)
	if err != nil {
		return nil, err
	}
	logf("got A pake msg (%v bytes)", len(msgA))

	msgB, mk, err := cpace.Exchange(pass, cpace.NewContextInfo("", "", nil), msgA)
	if err != nil {
		return nil, err
	}
	k := [32]byte{}
	if _, err := io.ReadFull(hkdf.New(sha256.New, mk, nil, nil), k[:]); err != nil {
		return nil, err
	}
	if err := writeBase64(ctx, ws, msgB); err != nil {
		return nil, err
	}
	logf("have key, sent B pake msg (%v bytes)", len(msgB))
	return &k, nil
}
