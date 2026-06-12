// Package relay connects a Fabrika daemon to a fabrika-portal relay server so
// a paired phone can reach it without the machine being exposed to the
// internet. The daemon dials out and holds one WebSocket tunnel; phone
// sessions are multiplexed over it, each end-to-end encrypted with the
// Noise-IK-style handshake implemented here — the relay only ever sees
// ciphertext.
package relay

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// Protocol label, mixed into the first transcript hash so handshakes from a
// different protocol or version can never be confused with ours.
const proto = "fabrika-relay/1"

// Wire tags (first byte of each message).
const (
	tagMsg1  = 0x01 // phone → daemon: ClientHello
	tagMsg2  = 0x02 // daemon → phone: ServerHello
	tagFrame = 0x03 // both: transport frame
)

const (
	keyLen     = 32
	tagLen     = 16 // poly1305
	frameCtrAt = 1  // frame layout: tag(1) || ctr(8) || ciphertext
	frameHdr   = 1 + 8

	// maxFrameCtr caps a session's frames per direction; hitting it closes
	// the session and the phone re-handshakes. Generous: sessions are
	// phone-lifetime, minutes to hours.
	maxFrameCtr = 1 << 32
)

var (
	ErrHandshake  = errors.New("relay: handshake failed")
	ErrFrame      = errors.New("relay: bad transport frame")
	ErrCtrReplay  = errors.New("relay: frame counter replayed or reordered")
	ErrCtrExhaust = errors.New("relay: frame counter exhausted")
)

// Msg1Payload rides encrypted inside ClientHello. Secret and Name are only
// set when the phone is pairing for the first time (from the QR payload).
type Msg1Payload struct {
	V      int    `json:"v"`
	Secret []byte `json:"secret,omitempty"`
	Name   string `json:"name,omitempty"`
}

// Msg2Payload rides encrypted inside ServerHello and tells the phone who it
// just authenticated to.
type Msg2Payload struct {
	V        int    `json:"v"`
	DeviceID string `json:"deviceId"`
	Project  string `json:"project"`
	Paired   bool   `json:"paired"`
}

// Session encrypts transport frames after a completed handshake. Not safe for
// concurrent use; callers serialize per direction (which also preserves the
// strictly-increasing counters the nonce scheme depends on).
type Session struct {
	send, recv       cipher.AEAD
	sendCtr, recvCtr uint64
}

// Seal encrypts one message into a transport frame.
func (s *Session) Seal(plaintext []byte) ([]byte, error) {
	s.sendCtr++
	if s.sendCtr >= maxFrameCtr {
		return nil, ErrCtrExhaust
	}
	frame := make([]byte, frameHdr, frameHdr+len(plaintext)+tagLen)
	frame[0] = tagFrame
	binary.BigEndian.PutUint64(frame[frameCtrAt:], s.sendCtr)
	return s.send.Seal(frame, ctrNonce(s.sendCtr), plaintext, nil), nil
}

// Open authenticates and decrypts a transport frame, enforcing strictly
// increasing counters (replay/reorder protection; ordering is guaranteed by
// the single-writer WebSocket hops).
func (s *Session) Open(frame []byte) ([]byte, error) {
	if len(frame) < frameHdr+tagLen || frame[0] != tagFrame {
		return nil, ErrFrame
	}
	ctr := binary.BigEndian.Uint64(frame[frameCtrAt:frameHdr])
	if ctr <= s.recvCtr {
		return nil, ErrCtrReplay
	}
	pt, err := s.recv.Open(nil, ctrNonce(ctr), frame[frameHdr:], nil)
	if err != nil {
		return nil, ErrFrame
	}
	s.recvCtr = ctr
	return pt, nil
}

// Keypair is a static or ephemeral X25519 key.
type Keypair struct {
	Priv [keyLen]byte
	Pub  [keyLen]byte
}

// GenerateKeypair makes a fresh X25519 keypair from rng (nil defaults to
// crypto/rand.Reader; deterministic readers drive tests/vectors).
func GenerateKeypair(rng io.Reader) (Keypair, error) {
	if rng == nil {
		rng = rand.Reader
	}
	var kp Keypair
	if _, err := io.ReadFull(rng, kp.Priv[:]); err != nil {
		return Keypair{}, err
	}
	pub, err := curve25519.X25519(kp.Priv[:], curve25519.Basepoint)
	if err != nil {
		return Keypair{}, err
	}
	copy(kp.Pub[:], pub)
	return kp, nil
}

// PublicKey derives the X25519 public key for a stored private key.
func PublicKey(priv []byte) ([]byte, error) {
	return curve25519.X25519(priv, curve25519.Basepoint)
}

// DaemonID is the public, unguessable address of a daemon on the relay: the
// hex SHA-256 fingerprint of its static public key (64 lowercase hex chars).
func DaemonID(pub []byte) string {
	sum := sha256.Sum256(pub)
	return fmt.Sprintf("%x", sum[:])
}

// Authorizer decides whether the phone behind a ClientHello may attach: a
// known paired device (by static pubkey), or a first pairing carrying a valid
// one-time secret. Returns the device id and whether this was a new pairing.
type Authorizer func(phonePub [keyLen]byte, payload Msg1Payload) (deviceID string, newlyPaired, ok bool)

// Respond processes a ClientHello on the daemon side. On success it returns
// the ServerHello to send back, the established session, and the device id
// that authenticated. rng supplies the ephemeral key.
func Respond(rng io.Reader, msg1 []byte, daemonID string, staticPriv [keyLen]byte, project string, authorize Authorizer) (msg2 []byte, sess *Session, deviceID string, err error) {
	// msg1 = tag(1) || PE(32) || encPS(32+16) || encPayload(>=16)
	if len(msg1) < 1+keyLen+keyLen+tagLen+tagLen || msg1[0] != tagMsg1 {
		return nil, nil, "", ErrHandshake
	}
	var phoneEph [keyLen]byte
	copy(phoneEph[:], msg1[1:1+keyLen])
	encPS := msg1[1+keyLen : 1+keyLen+keyLen+tagLen]
	encPayload := msg1[1+keyLen+keyLen+tagLen:]

	h1 := hash(concat([]byte(proto), []byte(daemonID), phoneEph[:]))
	dh1, err := curve25519.X25519(staticPriv[:], phoneEph[:]) // es
	if err != nil {
		return nil, nil, "", ErrHandshake
	}
	k1 := deriveKey(dh1, h1[:], "m1.static")
	psBytes, err := newAEAD(k1).Open(nil, zeroNonce(), encPS, h1[:])
	if err != nil {
		return nil, nil, "", ErrHandshake
	}
	var phoneStatic [keyLen]byte
	copy(phoneStatic[:], psBytes)

	h2 := hash(concat(h1[:], encPS))
	dh2, err := curve25519.X25519(staticPriv[:], phoneStatic[:]) // ss
	if err != nil {
		return nil, nil, "", ErrHandshake
	}
	k2 := deriveKey(concat(dh1, dh2), h2[:], "m1.payload")
	payloadJSON, err := newAEAD(k2).Open(nil, zeroNonce(), encPayload, h2[:])
	if err != nil {
		return nil, nil, "", ErrHandshake
	}
	var payload Msg1Payload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, nil, "", ErrHandshake
	}

	deviceID, newlyPaired, ok := authorize(phoneStatic, payload)
	if !ok {
		return nil, nil, "", ErrHandshake
	}

	eph, err := GenerateKeypair(rng)
	if err != nil {
		return nil, nil, "", err
	}
	h3 := hash(concat(h2[:], encPayload, eph.Pub[:]))
	dh3, err := curve25519.X25519(eph.Priv[:], phoneEph[:]) // ee
	if err != nil {
		return nil, nil, "", ErrHandshake
	}
	dh4, err := curve25519.X25519(eph.Priv[:], phoneStatic[:]) // se
	if err != nil {
		return nil, nil, "", ErrHandshake
	}
	kc2d, kd2c := sessionKeys(dh1, dh2, dh3, dh4, h3[:])

	m2, err := json.Marshal(Msg2Payload{V: 1, DeviceID: deviceID, Project: project, Paired: newlyPaired})
	if err != nil {
		return nil, nil, "", err
	}
	encM2 := newAEAD(kd2c).Seal(nil, zeroNonce(), m2, h3[:])

	out := make([]byte, 0, 1+keyLen+len(encM2))
	out = append(out, tagMsg2)
	out = append(out, eph.Pub[:]...)
	out = append(out, encM2...)

	return out, &Session{send: newAEAD(kd2c), recv: newAEAD(kc2d)}, deviceID, nil
}

// InitiatorState carries the initiator's progress between Initiate and Finish.
type InitiatorState struct {
	phoneStatic Keypair
	phoneEph    Keypair
	h2          [32]byte
	encPayload  []byte
	dh1, dh2    []byte
}

// Initiate builds a ClientHello on the phone side. The Go implementation is
// the protocol reference: it drives the daemon's tests and generates the
// vectors the TypeScript client is validated against.
func Initiate(rng io.Reader, daemonID string, daemonPub [keyLen]byte, phoneStatic Keypair, payload Msg1Payload) ([]byte, *InitiatorState, error) {
	eph, err := GenerateKeypair(rng)
	if err != nil {
		return nil, nil, err
	}

	h1 := hash(concat([]byte(proto), []byte(daemonID), eph.Pub[:]))
	dh1, err := curve25519.X25519(eph.Priv[:], daemonPub[:]) // es
	if err != nil {
		return nil, nil, err
	}
	k1 := deriveKey(dh1, h1[:], "m1.static")
	encPS := newAEAD(k1).Seal(nil, zeroNonce(), phoneStatic.Pub[:], h1[:])

	h2 := hash(concat(h1[:], encPS))
	dh2, err := curve25519.X25519(phoneStatic.Priv[:], daemonPub[:]) // ss
	if err != nil {
		return nil, nil, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	k2 := deriveKey(concat(dh1, dh2), h2[:], "m1.payload")
	encPayload := newAEAD(k2).Seal(nil, zeroNonce(), payloadJSON, h2[:])

	msg1 := make([]byte, 0, 1+keyLen+len(encPS)+len(encPayload))
	msg1 = append(msg1, tagMsg1)
	msg1 = append(msg1, eph.Pub[:]...)
	msg1 = append(msg1, encPS...)
	msg1 = append(msg1, encPayload...)

	return msg1, &InitiatorState{
		phoneStatic: phoneStatic,
		phoneEph:    eph,
		h2:          h2,
		encPayload:  encPayload,
		dh1:         dh1,
		dh2:         dh2,
	}, nil
}

// Finish processes the ServerHello and establishes the session. A successful
// decrypt authenticates the daemon (only the static private key holder can
// derive the session keys).
func (st *InitiatorState) Finish(msg2 []byte) (*Session, Msg2Payload, error) {
	if len(msg2) < 1+keyLen+tagLen || msg2[0] != tagMsg2 {
		return nil, Msg2Payload{}, ErrHandshake
	}
	var daemonEph [keyLen]byte
	copy(daemonEph[:], msg2[1:1+keyLen])
	encM2 := msg2[1+keyLen:]

	h3 := hash(concat(st.h2[:], st.encPayload, daemonEph[:]))
	dh3, err := curve25519.X25519(st.phoneEph.Priv[:], daemonEph[:]) // ee
	if err != nil {
		return nil, Msg2Payload{}, ErrHandshake
	}
	dh4, err := curve25519.X25519(st.phoneStatic.Priv[:], daemonEph[:]) // se
	if err != nil {
		return nil, Msg2Payload{}, ErrHandshake
	}
	kc2d, kd2c := sessionKeys(st.dh1, st.dh2, dh3, dh4, h3[:])

	m2JSON, err := newAEAD(kd2c).Open(nil, zeroNonce(), encM2, h3[:])
	if err != nil {
		return nil, Msg2Payload{}, ErrHandshake
	}
	var m2 Msg2Payload
	if err := json.Unmarshal(m2JSON, &m2); err != nil {
		return nil, Msg2Payload{}, ErrHandshake
	}
	return &Session{send: newAEAD(kc2d), recv: newAEAD(kd2c)}, m2, nil
}

// --- primitives ---

func hash(b []byte) [32]byte { return sha256.Sum256(b) }

func concat(parts ...[]byte) []byte {
	var n int
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func deriveKey(ikm, salt []byte, info string) []byte {
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte(info)), key); err != nil {
		panic("hkdf: " + err.Error()) // cannot fail for 32-byte reads
	}
	return key
}

// sessionKeys derives the two directional transport keys from the full DH
// transcript: kc2d encrypts phone→daemon, kd2c daemon→phone.
func sessionKeys(dh1, dh2, dh3, dh4, h3 []byte) (kc2d, kd2c []byte) {
	ck := deriveKey(concat(dh1, dh2, dh3, dh4), h3, "session")
	return deriveKey(ck, nil, "c2d"), deriveKey(ck, nil, "d2c")
}

func newAEAD(key []byte) cipher.AEAD {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		panic("chacha20poly1305: " + err.Error()) // key is always 32 bytes
	}
	return a
}

func zeroNonce() []byte { return make([]byte, chacha20poly1305.NonceSize) }

func ctrNonce(ctr uint64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(n[4:], ctr)
	return n
}

// NewPairingSecret mints the one-time secret embedded in a pairing QR.
func NewPairingSecret() ([]byte, error) {
	s := make([]byte, 16)
	if _, err := rand.Read(s); err != nil {
		return nil, err
	}
	return s, nil
}
