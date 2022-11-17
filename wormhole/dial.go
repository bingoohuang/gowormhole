// Package wormhole implements a signalling protocol to establish password protected
// WebRTC connections between peers.
//
// WebRTC uses DTLS-SRTP (https://tools.ietf.org/html/rfc5764) to secure its
// data. The mechanism it uses to exchange keys relies on exchanging metadata
// that includes both endpoints' certificate fingerprints via some trusted channel,
// typically a signalling server over https and websockets. More in RFC5763
// (https://tools.ietf.org/html/rfc5763).
//
// This package removes the signalling server from the trust model by using a
// PAKE to estabish the authenticity of the WebRTC metadata. In other words,
// it's a clone of Magic Wormhole made to use WebRTC as the transport.
//
// The protocol requires a signalling server that facilitates exchanging
// arbitrary messages via a slot system. The server subcommand of the
// gowormhole tool is an implementation of this over WebSockets.
//
// Rough sketch of the handshake:
//
//	Peer               Signalling Server                Peer
//	----open------------------> |
//	<---new_slot,TURN_ticket--- |
//	                            | <------------------open----
//	                            | ------------TURN_ticket--->
//	<---------------------------|--------------pake_msg_a----
//	----pake_msg_b--------------|--------------------------->
//	----sbox(offer)-------------|--------------------------->
//	<---------------------------|------------sbox(answer)----
//	----sbox(candidates...)-----|--------------------------->
//	<---------------------------|-----sbox(candidates...)----
package wormhole

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/bingoohuang/gg/pkg/defaults"
	"github.com/bingoohuang/gg/pkg/ss"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wordlist"
	"github.com/pion/webrtc/v3"
	"golang.org/x/net/proxy"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Protocol is an identifier for the current signalling scheme. It's
// intended to help clients print a friendlier message urging them to
// upgrade if the signalling server has a different version.
const Protocol = "4"

const (
	// CloseNoSuchSlot is the WebSocket status returned if the slot is not valid.
	CloseNoSuchSlot = 4000 + iota

	// CloseSlotTimedOut is the WebSocket status returned when the slot times out.
	CloseSlotTimedOut

	// CloseNoMoreSlots is the WebSocket status returned when the signalling server
	// cannot allocate any new slots at the time.
	CloseNoMoreSlots

	// CloseWrongProto is the WebSocket status returned when the signalling server
	// runs a different version of the signalling protocol.
	CloseWrongProto

	// ClosePeerHungUp is the WebSocket status returned when the peer has closed
	// its connection.
	ClosePeerHungUp

	// CloseBadKey is the WebSocket status returned when the peer has closed its
	// connection because the key it derived is bad.
	CloseBadKey

	// CloseWebRTCSuccess indicates a WebRTC connection was successful.
	CloseWebRTCSuccess

	// CloseWebRTCSuccessDirect indicates a WebRTC connection was successful and we
	// know it's peer-to-peer.
	CloseWebRTCSuccessDirect

	// CloseWebRTCSuccessRelay indicates a WebRTC connection was successful and we
	// know it's going via a relay.
	CloseWebRTCSuccessRelay

	// CloseWebRTCFailed we couldn't establish a WebRTC connection.
	CloseWebRTCFailed
)

var (
	// ErrBadVersion is returned when the signalling server runs an incompatible
	// version of the signalling protocol.
	ErrBadVersion = errors.New("bad version")

	// ErrBadKey is returned when the peer on the same slot uses a different password.
	ErrBadKey = errors.New("bad key")

	// ErrTimedOut indicates signalling has timed out.
	ErrTimedOut = errors.New("timed out")
)

// Verbose logging.
var Verbose = false

func logf(format string, v ...interface{}) {
	if Verbose {
		log.Printf(format, v...)
	}
}

func Setup(ctx context.Context, slot, pass, sigserv string, timeouts *Timeouts) (*Wormhole, error) {
	ir, err := initPeerConnection(ctx, slot, pass, sigserv, timeouts)
	if err != nil {
		return nil, err
	}
	if ir.Exists {
		err = joinWormhole(ctx, ir, pass)
	} else {
		err = newWormhole(ctx, ir, pass)
	}

	if err != nil {
		return nil, err
	}

	return ir.Wormhole, nil
}

// A Wormhole is a WebRTC connection established via the WebWormhole signalling
// protocol. It is wraps webrtc.PeerConnection and webrtc.DataChannel.
//
// BUG(s): A PeerConnection established via Wormhole will always have a DataChannel
// created for it, with the name "data" and id 0.
type Wormhole struct {
	rwc io.ReadWriteCloser
	d   *webrtc.DataChannel
	pc  *webrtc.PeerConnection

	// opened signals that the underlying DataChannel is open and ready
	// to handle data.
	opened chan struct{}
	// err forwards errors from the OnError callback.
	err chan error
	// flushc is a condition variable to coordinate flushed state of the
	// underlying channel.
	flushc *sync.Cond

	// code for the current
	Code     string
	Timeouts *Timeouts
}

// Read writes a message to the default DataChannel.
func (c *Wormhole) Write(p []byte) (n int, err error) {
	// The webrtc package's channel does not have a blocking Write, so
	// we can't just use io.Copy until the issue is fixed upsteam.
	// Work around this by blocking here and waiting for flushes.
	// https://github.com/pion/sctp/issues/77
	c.flushc.L.Lock()
	for c.d.BufferedAmount() > c.d.BufferedAmountLowThreshold() {
		c.flushc.Wait()
	}
	c.flushc.L.Unlock()
	return c.rwc.Write(p)
}

// Read a message from the default DataChannel.
func (c *Wormhole) Read(p []byte) (n int, err error) {
	return c.rwc.Read(p)
}

// TODO benchmark this buffer madness.
func (c *Wormhole) flushed() {
	c.flushc.L.Lock()
	c.flushc.Signal()
	c.flushc.L.Unlock()
}

// Close attempts to flush the DataChannel buffers then close it
// and its PeerConnection.
func (c *Wormhole) Close() (err error) {
	logf("Wormhole is closing")

	startTime := time.Now()
	for c.d.BufferedAmount() > 0 && time.Since(startTime) < c.Timeouts.CloseTimeout.D() {
		// SetBufferedAmountLowThreshold does not seem to take effect  when after the last Write().
		time.Sleep(time.Second) // eww.
	}
	tryclose := func(c io.Closer) {
		if e := c.Close(); e != nil {
			err = e
		}
	}
	defer tryclose(c.pc)
	defer tryclose(c.d)
	defer tryclose(c.rwc)
	return nil
}

func (c *Wormhole) open() {
	var err error
	if c.rwc, err = c.d.Detach(); err != nil {
		c.err <- err
		return
	}
	close(c.opened)
}

// It's not really clear to me when this will be invoked.
func (c *Wormhole) error(err error) {
	log.Printf("debug: %v", err)
	c.err <- err
}

type InitMsg struct {
	Exists     bool               `json:"exists,omitempty"`
	Slot       string             `json:"slot,omitempty"`
	ICEServers []webrtc.ICEServer `json:"iceServers,omitempty"`
}

// handleRemoteCandidates waits for remote candidate to trickle in. We close
// the websocket when we get a successful connection so this should fail and
// exit at some point.
func (c *Wormhole) handleRemoteCandidates(ctx context.Context, ws *websocket.Conn, key *[32]byte) {
	for {
		var candidate webrtc.ICECandidateInit
		if _, err := readEncJSON(ctx, ws, key, &candidate); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				logf("cannot read remote candidate: %v", err)
			}
			return
		}

		logf("recv remote candidate: %v", candidate.Candidate)

		if err := c.pc.AddICECandidate(candidate); err != nil {
			logf("cannot add candidate: %v", err)
			return
		}
	}
}

// https://github.com/pion/ice/blob/master/agent_config.go
// * disconnectedTimeout is the duration without network activity before a Agent is considered disconnected. Default is 5 Seconds
// * failedTimeout is the duration without network activity before a Agent is considered failed after disconnected. Default is 25 Seconds
// * keepAliveInterval is how often the ICE Agent sends extra traffic if there is no activity, if media is flowing no traffic will be sent. Default is 2 seconds

type Timeouts struct {
	DisconnectedTimeout util.Duration `json:"disconnectedTimeout" default:"5s"`
	FailedTimeout       util.Duration `json:"failedTimeout" default:"10s"`
	KeepAliveInterval   util.Duration `json:"keepAliveInterval" default:"2s"`
	// CloseTimeout set the timeout for the closing, see Wormhole.Close
	CloseTimeout util.Duration `json:"closeTimeout" default:"10s"`
	// RwTimeout set the read/write timeout for data channel io.
	RwTimeout util.Duration `json:"rwTimeout" default:"10s"`
}

func (c *Wormhole) newPeerConnection(ice []webrtc.ICEServer) (err error) {
	// Accessing pion/webrtc APIs like DataChannel.Detach() requires that we do this voodoo.
	s := webrtc.SettingEngine{}
	s.SetICETimeouts(c.Timeouts.DisconnectedTimeout.D(), c.Timeouts.FailedTimeout.D(), c.Timeouts.KeepAliveInterval.D())
	s.DetachDataChannels()
	s.SetICEProxyDialer(proxy.FromEnvironment())
	rtcapi := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	if c.pc, err = rtcapi.NewPeerConnection(webrtc.Configuration{ICEServers: ice}); err != nil {
		return err
	}

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	c.pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("Peer Connection State has changed: %s", s)

		// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
		// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
		// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
	})

	sigh := true
	c.d, err = c.pc.CreateDataChannel("data", &webrtc.DataChannelInit{Negotiated: &sigh, ID: new(uint16)})
	if err != nil {
		return err
	}
	c.d.OnOpen(c.open)
	c.d.OnError(c.error)
	c.d.OnBufferedAmountLow(c.flushed)
	// Any threshold amount >= 1MiB seems to occasionally lock up pion.
	// Choose 512 KiB as a safe default.
	c.d.SetBufferedAmountLowThreshold(512 << 10)
	return nil
}

// IsRelay returns whether the peer connection is over a TURN relay server or not.
func (c *Wormhole) IsRelay() bool {
	stats := c.pc.GetStats()
	for _, s := range stats {
		pairstats, ok := s.(webrtc.ICECandidatePairStats)
		if !ok || !pairstats.Nominated {
			continue
		}
		local, ok := stats[pairstats.LocalCandidateID].(webrtc.ICECandidateStats)
		if !ok {
			continue
		}
		remote, ok := stats[pairstats.RemoteCandidateID].(webrtc.ICECandidateStats)
		if !ok {
			continue
		}
		if remote.CandidateType == webrtc.ICECandidateTypeRelay ||
			local.CandidateType == webrtc.ICECandidateTypeRelay {
			return true
		}
	}
	return false
}

// New starts a new signalling handshake after asking the server to allocate
// a new slot.
//
// The slot is used to synchronise with the remote peer on signalling server
// sigserv, and pass is used as the PAKE password authenticate the WebRTC
// offer and answer.
//
// The server generated slot identifier is written on slotc.
//
// If pc is nil it initialises ones using the default STUN server.
func newWormhole(ctx context.Context, ir *initPeerConnectionResult, pass string) error {
	key, err := exhangeKeySideB(ctx, ir.Ws, pass)
	if err != nil {
		return err
	}

	onICECandidate(ctx, ir, key)

	if err := sendOffer(ctx, ir, key); err != nil {
		return err
	}

	if err := recvAnwser(ctx, ir, key); err != nil {
		return err
	}

	return waitDataChannelOpen(ctx, ir.Wormhole, ir.Ws, key)
}

// joinWormhole performs the signalling handshake to join an existing slot.
//
// slot is used to synchronise with the remote peer on signalling server
// sigserv, and pass is used as the PAKE password authenticate the WebRTC
// offer and answer.
//
// If pc is nil it initialises ones using the default STUN server.
func joinWormhole(ctx context.Context, ir *initPeerConnectionResult, pass string) error {
	key, err := exchangeKeySideA(ctx, ir.Ws, pass)
	if err != nil {
		return err
	}

	onICECandidate(ctx, ir, key)

	if err := recvOffer(ctx, ir, key); err != nil {
		return err
	}
	if err := sendAnswer(ctx, ir, key); err != nil {
		return err
	}

	return waitDataChannelOpen(ctx, ir.Wormhole, ir.Ws, key)
}

func onICECandidate(ctx context.Context, ir *initPeerConnectionResult, key *[32]byte) {
	ir.Wormhole.pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		logf("sent local candidate: %v", candidate.String())
		if _, err := writeEncJSON(ctx, ir.Ws, key, candidate.ToJSON()); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				logf("cannot send local candidate: %v", err)
			}
			return
		}
	})
}

func sendOffer(ctx context.Context, ir *initPeerConnectionResult, key *[32]byte) error {
	offer, err := ir.Wormhole.pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("CreateOffer failed: %w", err)
	}
	offerJSON, err := writeEncJSON(ctx, ir.Ws, key, offer)
	if err != nil {
		return fmt.Errorf("writeEncJSON failed: %w", err)
	}
	if err := ir.Wormhole.pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("SetLocalDescription failed: %w", err)
	}
	logf("sent offer JSON: %s", offerJSON)
	logf("sent offer BASE64: %s", base64.StdEncoding.EncodeToString(offerJSON))

	return nil
}

func recvOffer(ctx context.Context, ir *initPeerConnectionResult, key *[32]byte) error {
	var offer webrtc.SessionDescription
	offerJSON, err := readEncJSON(ctx, ir.Ws, key, &offer)
	if err != nil {
		if err == ErrBadKey {
			// Close with the right status so the other side knows to quit immediately.
			_ = ir.Ws.Close(CloseBadKey, "bad key")
		}
		return fmt.Errorf("readEncJSON failed: %w", err)
	}

	if err := ir.Wormhole.pc.SetRemoteDescription(offer); err != nil {
		return fmt.Errorf("SetRemoteDescription failed: %w", err)
	}
	logf("got offer JSON: %s", offerJSON)
	logf("got offer BASE64: %s", base64.StdEncoding.EncodeToString(offerJSON))

	return nil
}

func sendAnswer(ctx context.Context, ir *initPeerConnectionResult, key *[32]byte) error {
	answer, err := ir.Wormhole.pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("CreateAnswer failed: %w", err)
	}
	answerJSON, err := writeEncJSON(ctx, ir.Ws, key, answer)
	if err != nil {
		return fmt.Errorf("writeEncJSON failed: %w", err)
	}

	if err := ir.Wormhole.pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("SetLocalDescription failed: %w", err)
	}

	logf("sent answer JSON: %s", answerJSON)
	logf("sent answer BASE64: %s", base64.StdEncoding.EncodeToString(answerJSON))

	return nil
}

func recvAnwser(ctx context.Context, ir *initPeerConnectionResult, key *[32]byte) error {
	var answer webrtc.SessionDescription
	answerJSON, err := readEncJSON(ctx, ir.Ws, key, &answer)
	if err != nil {
		if err == ErrBadKey {
			// Close with the right status so the other side knows to quit immediately.
			_ = ir.Ws.Close(CloseBadKey, "bad key")
		}
		return fmt.Errorf("readEncJSON failed: %w", err)
	}
	if err := ir.Wormhole.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("SetRemoteDescription failed: %w", err)
	}
	logf("got answer JSON: %s", answerJSON)
	logf("got answer BASE64: %s", base64.StdEncoding.EncodeToString(answerJSON))
	return nil
}

func waitDataChannelOpen(ctx context.Context, c *Wormhole, ws *websocket.Conn, key *[32]byte) error {
	go c.handleRemoteCandidates(ctx, ws, key)

	timeout := 15 * time.Second
	select {
	case <-c.opened:
		relay := c.IsRelay()
		code := util.If[websocket.StatusCode](relay, CloseWebRTCSuccessRelay, CloseWebRTCSuccessDirect)
		_ = ws.Close(code, "")
		logf("webrtc connection succeeded (relay: %v) closing signalling channel", relay)
		return nil
	case err := <-c.err:
		_ = ws.Close(CloseWebRTCFailed, "")
		log.Printf("waitDataChannelOpen failed: %v", err)
		return err
	case <-time.After(30 * time.Second):
		_ = ws.Close(CloseWebRTCFailed, "timed out")
		log.Printf("waitDataChannelOpen timed out in %s", timeout)
		return ErrTimedOut
	}
}

type initPeerConnectionResult struct {
	Ws       *websocket.Conn
	Wormhole *Wormhole
	Exists   bool
}

func initPeerConnection(ctx context.Context, slot, pass, sigserv string, timeouts *Timeouts) (*initPeerConnectionResult, error) {
	ws, err := dialWebsocket(ctx, slot, sigserv)
	if err != nil {
		return nil, err
	}

	// reads the first message the signalling server sends overthe WebSocket connection,
	// which has metadata includign assigned slot and ICE servers to use.
	initMsg := &InitMsg{}
	if err := wsjson.Read(ctx, ws, initMsg); err != nil {
		if websocket.CloseStatus(err) == CloseWrongProto {
			err = ErrBadVersion
		}
		return nil, fmt.Errorf("read InitMsg failed: %w", err)
	}

	logf("connected to signalling server, got %s slot: %v", ss.If(initMsg.Exists, "old", "new"), initMsg.Slot)

	slotNum, err := strconv.Atoi(initMsg.Slot)
	if err != nil {
		return nil, fmt.Errorf("got invalid slot %q from signalling server", initMsg.Slot)
	}

	if timeouts == nil {
		timeouts = &Timeouts{}
		defaults.Set(timeouts)
	}
	c := &Wormhole{
		opened:   make(chan struct{}),
		err:      make(chan error),
		flushc:   sync.NewCond(&sync.Mutex{}),
		Code:     wordlist.Encode(slotNum, []byte(pass)),
		Timeouts: timeouts,
	}
	log.Printf("Wormhole code: %s", c.Code)

	if err := c.newPeerConnection(initMsg.ICEServers); err != nil {
		return nil, err
	}

	return &initPeerConnectionResult{Ws: ws, Wormhole: c, Exists: initMsg.Exists}, nil
}
