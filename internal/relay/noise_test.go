package relay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// detRand is a deterministic byte stream for vectors: sha256(seed || ctr).
type detRand struct {
	seed []byte
	ctr  uint64
	buf  []byte
}

func (d *detRand) Read(p []byte) (int, error) {
	for len(d.buf) < len(p) {
		var c [8]byte
		binary.BigEndian.PutUint64(c[:], d.ctr)
		d.ctr++
		block := sha256.Sum256(append(append([]byte{}, d.seed...), c[:]...))
		d.buf = append(d.buf, block[:]...)
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

func mustKeypair(t *testing.T, rng io.Reader) Keypair {
	t.Helper()
	kp, err := GenerateKeypair(rng)
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return kp
}

// handshake runs a full pairing handshake and returns both sessions.
func handshake(t *testing.T, payload Msg1Payload, authorize Authorizer) (phone, daemon *Session, m2 Msg2Payload) {
	t.Helper()
	daemonKey := mustKeypair(t, rand.Reader)
	phoneKey := mustKeypair(t, rand.Reader)
	daemonID := DaemonID(daemonKey.Pub[:])

	msg1, st, err := Initiate(rand.Reader, daemonID, daemonKey.Pub, phoneKey, payload)
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	msg2, daemonSess, _, err := Respond(rand.Reader, msg1, daemonID, daemonKey.Priv, "myproject", authorize)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	phoneSess, m2, err := st.Finish(msg2)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return phoneSess, daemonSess, m2
}

func TestHandshakeAndFrames(t *testing.T) {
	secret := []byte("one-time-pairing")
	phone, daemon, m2 := handshake(t,
		Msg1Payload{V: 1, Secret: secret, Name: "test phone"},
		func(pub [32]byte, p Msg1Payload) (string, bool, bool) {
			if !bytes.Equal(p.Secret, secret) || p.Name != "test phone" {
				return "", false, false
			}
			return "device-1", true, true
		},
	)
	if m2.DeviceID != "device-1" || !m2.Paired || m2.Project != "myproject" {
		t.Fatalf("unexpected msg2 payload: %+v", m2)
	}

	// Both directions, multiple frames.
	for i := 0; i < 3; i++ {
		f, err := phone.Seal([]byte("ping from phone"))
		if err != nil {
			t.Fatalf("phone Seal: %v", err)
		}
		pt, err := daemon.Open(f)
		if err != nil || string(pt) != "ping from phone" {
			t.Fatalf("daemon Open: %v %q", err, pt)
		}
		f, err = daemon.Seal([]byte("pong from daemon"))
		if err != nil {
			t.Fatalf("daemon Seal: %v", err)
		}
		pt, err = phone.Open(f)
		if err != nil || string(pt) != "pong from daemon" {
			t.Fatalf("phone Open: %v %q", err, pt)
		}
	}
}

func TestKnownDeviceSkipsSecret(t *testing.T) {
	known := mustKeypair(t, rand.Reader)
	daemonKey := mustKeypair(t, rand.Reader)
	daemonID := DaemonID(daemonKey.Pub[:])

	msg1, st, err := Initiate(rand.Reader, daemonID, daemonKey.Pub, known, Msg1Payload{V: 1})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	authorize := func(pub [32]byte, p Msg1Payload) (string, bool, bool) {
		if pub == known.Pub {
			return "device-known", false, true
		}
		return "", false, false
	}
	msg2, _, deviceID, err := Respond(rand.Reader, msg1, daemonID, daemonKey.Priv, "p", authorize)
	if err != nil || deviceID != "device-known" {
		t.Fatalf("Respond: %v device=%q", err, deviceID)
	}
	if _, m2, err := st.Finish(msg2); err != nil || m2.Paired {
		t.Fatalf("Finish: %v paired=%v", err, m2.Paired)
	}
}

func TestRejectsWrongSecret(t *testing.T) {
	daemonKey := mustKeypair(t, rand.Reader)
	phoneKey := mustKeypair(t, rand.Reader)
	daemonID := DaemonID(daemonKey.Pub[:])

	msg1, _, err := Initiate(rand.Reader, daemonID, daemonKey.Pub, phoneKey, Msg1Payload{V: 1, Secret: []byte("wrong")})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	deny := func([32]byte, Msg1Payload) (string, bool, bool) { return "", false, false }
	if _, _, _, err := Respond(rand.Reader, msg1, daemonID, daemonKey.Priv, "p", deny); !errors.Is(err, ErrHandshake) {
		t.Fatalf("expected ErrHandshake, got %v", err)
	}
}

func TestRejectsWrongDaemonKey(t *testing.T) {
	daemonKey := mustKeypair(t, rand.Reader)
	otherKey := mustKeypair(t, rand.Reader)
	phoneKey := mustKeypair(t, rand.Reader)
	daemonID := DaemonID(daemonKey.Pub[:])

	// Phone encrypts to the wrong static key (e.g. MITM relay substituting
	// its own): the real daemon must fail to decrypt.
	msg1, _, err := Initiate(rand.Reader, daemonID, otherKey.Pub, phoneKey, Msg1Payload{V: 1})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	allow := func([32]byte, Msg1Payload) (string, bool, bool) { return "d", false, true }
	if _, _, _, err := Respond(rand.Reader, msg1, daemonID, daemonKey.Priv, "p", allow); !errors.Is(err, ErrHandshake) {
		t.Fatalf("expected ErrHandshake, got %v", err)
	}
}

func TestTamperedMsg2(t *testing.T) {
	daemonKey := mustKeypair(t, rand.Reader)
	phoneKey := mustKeypair(t, rand.Reader)
	daemonID := DaemonID(daemonKey.Pub[:])

	msg1, st, err := Initiate(rand.Reader, daemonID, daemonKey.Pub, phoneKey, Msg1Payload{V: 1})
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	allow := func([32]byte, Msg1Payload) (string, bool, bool) { return "d", false, true }
	msg2, _, _, err := Respond(rand.Reader, msg1, daemonID, daemonKey.Priv, "p", allow)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	msg2[len(msg2)-1] ^= 0xff
	if _, _, err := st.Finish(msg2); !errors.Is(err, ErrHandshake) {
		t.Fatalf("expected ErrHandshake on tampered msg2, got %v", err)
	}
}

func TestTamperedAndReplayedFrames(t *testing.T) {
	allow := func([32]byte, Msg1Payload) (string, bool, bool) { return "d", true, true }
	phone, daemon, _ := handshake(t, Msg1Payload{V: 1, Secret: []byte("s")}, allow)

	frame, err := phone.Seal([]byte("payload"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Tampered ciphertext.
	bad := append([]byte{}, frame...)
	bad[len(bad)-1] ^= 0xff
	if _, err := daemon.Open(bad); !errors.Is(err, ErrFrame) {
		t.Fatalf("expected ErrFrame, got %v", err)
	}

	// Genuine frame still opens (tamper attempt must not advance state).
	if _, err := daemon.Open(frame); err != nil {
		t.Fatalf("Open after tamper attempt: %v", err)
	}

	// Replay of the same frame.
	if _, err := daemon.Open(frame); !errors.Is(err, ErrCtrReplay) {
		t.Fatalf("expected ErrCtrReplay, got %v", err)
	}
}

// --- cross-language vectors ---

// vectors is the contract between this Go reference implementation and the
// portal PWA's TypeScript client (copied into fabrika-portal web/test/).
type vectors struct {
	DaemonPriv      string `json:"daemonPriv"`
	DaemonPub       string `json:"daemonPub"`
	DaemonID        string `json:"daemonId"`
	PhoneStaticPriv string `json:"phoneStaticPriv"`
	PhoneStaticPub  string `json:"phoneStaticPub"`
	PhoneEphPriv    string `json:"phoneEphPriv"`
	DaemonEphPriv   string `json:"daemonEphPriv"`
	Msg1PayloadJSON string `json:"msg1PayloadJson"`
	Project         string `json:"project"`
	DeviceID        string `json:"deviceId"`
	Msg1            string `json:"msg1"`
	Msg2            string `json:"msg2"`
	PhoneFrame1     string `json:"phoneFrame1"` // Seal("hello from phone") ctr=1
	DaemonFrame1    string `json:"daemonFrame1"`
	PhoneFrame2     string `json:"phoneFrame2"`
}

func buildVectors(t *testing.T) vectors {
	t.Helper()
	daemonKey := mustKeypair(t, &detRand{seed: []byte("daemon-static")})
	phoneKey := mustKeypair(t, &detRand{seed: []byte("phone-static")})
	phoneEph := &detRand{seed: []byte("phone-eph")}
	daemonEph := &detRand{seed: []byte("daemon-eph")}
	daemonID := DaemonID(daemonKey.Pub[:])
	secret := []byte("0123456789abcdef")

	payload := Msg1Payload{V: 1, Secret: secret, Name: "vector phone"}
	payloadJSON, _ := json.Marshal(payload)

	msg1, st, err := Initiate(phoneEph, daemonID, daemonKey.Pub, phoneKey, payload)
	if err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	authorize := func(pub [32]byte, p Msg1Payload) (string, bool, bool) {
		if !bytes.Equal(p.Secret, secret) {
			return "", false, false
		}
		return "vector-device", true, true
	}
	msg2, daemonSess, _, err := Respond(daemonEph, msg1, daemonID, daemonKey.Priv, "vectorproj", authorize)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	phoneSess, _, err := st.Finish(msg2)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	pf1, _ := phoneSess.Seal([]byte("hello from phone"))
	df1, _ := daemonSess.Seal([]byte("hello from daemon"))
	pf2, _ := phoneSess.Seal([]byte("second frame"))
	if _, err := daemonSess.Open(pf1); err != nil {
		t.Fatalf("self-check open: %v", err)
	}

	// Re-derive the ephemeral privs the deterministic readers produced.
	ephPriv := func(seed string) string {
		kp, _ := GenerateKeypair(&detRand{seed: []byte(seed)})
		return hex.EncodeToString(kp.Priv[:])
	}

	return vectors{
		DaemonPriv:      hex.EncodeToString(daemonKey.Priv[:]),
		DaemonPub:       hex.EncodeToString(daemonKey.Pub[:]),
		DaemonID:        daemonID,
		PhoneStaticPriv: hex.EncodeToString(phoneKey.Priv[:]),
		PhoneStaticPub:  hex.EncodeToString(phoneKey.Pub[:]),
		PhoneEphPriv:    ephPriv("phone-eph"),
		DaemonEphPriv:   ephPriv("daemon-eph"),
		Msg1PayloadJSON: string(payloadJSON),
		Project:         "vectorproj",
		DeviceID:        "vector-device",
		Msg1:            hex.EncodeToString(msg1),
		Msg2:            hex.EncodeToString(msg2),
		PhoneFrame1:     hex.EncodeToString(pf1),
		DaemonFrame1:    hex.EncodeToString(df1),
		PhoneFrame2:     hex.EncodeToString(pf2),
	}
}

// TestVectors pins the wire format: it fails if the implementation drifts
// from testdata/vectors.json. Regenerate with FABRIKA_WRITE_VECTORS=1.
func TestVectors(t *testing.T) {
	got := buildVectors(t)
	path := filepath.Join("testdata", "vectors.json")

	if os.Getenv("FABRIKA_WRITE_VECTORS") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors (run with FABRIKA_WRITE_VECTORS=1 to generate): %v", err)
	}
	var want vectors
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if want != got {
		t.Fatalf("implementation drifted from checked-in vectors\nwant: %+v\ngot:  %+v", want, got)
	}
}
