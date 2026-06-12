package store

import (
	"bytes"
	"testing"

	"github.com/berkaycubuk/fabrika/internal/model"
)

func TestRelayIdentityRoundTrip(t *testing.T) {
	s := openTest(t)

	key, err := s.Relay.Identity("/repo/a")
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if key != nil {
		t.Fatalf("expected no identity yet, got %x", key)
	}

	priv := bytes.Repeat([]byte{7}, 32)
	if err := s.Relay.SaveIdentity("/repo/a", priv); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	key, err = s.Relay.Identity("/repo/a")
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if !bytes.Equal(key, priv) {
		t.Fatalf("identity mismatch: got %x", key)
	}

	// Identities are per project root.
	if key, _ := s.Relay.Identity("/repo/b"); key != nil {
		t.Fatal("identity should not leak across projects")
	}
}

func TestRelayDevices(t *testing.T) {
	s := openTest(t)
	const daemon = "abc123"

	d := &model.RelayDevice{DaemonID: daemon, PubKey: bytes.Repeat([]byte{1}, 32), Name: "Berkay's phone"}
	if err := s.Relay.AddDevice(d); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if d.ID == "" {
		t.Fatal("AddDevice should assign an ID")
	}

	// Re-pairing the same pubkey keeps the ID and refreshes the name.
	again := &model.RelayDevice{DaemonID: daemon, PubKey: d.PubKey, Name: "renamed"}
	if err := s.Relay.AddDevice(again); err != nil {
		t.Fatalf("AddDevice upsert: %v", err)
	}
	if again.ID != d.ID {
		t.Fatalf("upsert should keep ID: %s != %s", again.ID, d.ID)
	}

	devices, err := s.Relay.Devices(daemon)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "renamed" || !bytes.Equal(devices[0].PubKey, d.PubKey) {
		t.Fatalf("unexpected devices: %+v", devices)
	}

	// Other daemons see nothing.
	if devices, _ := s.Relay.Devices("other"); len(devices) != 0 {
		t.Fatalf("devices leaked across daemons: %+v", devices)
	}

	if err := s.Relay.TouchDevice(d.ID); err != nil {
		t.Fatalf("TouchDevice: %v", err)
	}

	if err := s.Relay.DeleteDevice(d.ID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if err := s.Relay.DeleteDevice(d.ID); err != ErrNotFound {
		t.Fatalf("double delete should be ErrNotFound, got %v", err)
	}
}

func TestRelayPushSubs(t *testing.T) {
	s := openTest(t)
	const daemon = "abc123"

	d := &model.RelayDevice{DaemonID: daemon, PubKey: bytes.Repeat([]byte{2}, 32)}
	if err := s.Relay.AddDevice(d); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	sub := &model.RelayPushSub{
		Endpoint: "https://push.example/v1/abc",
		DaemonID: daemon,
		DeviceID: d.ID,
		P256DH:   "p256dh-key",
		Auth:     "auth-secret",
	}
	if err := s.Relay.AddPushSub(sub); err != nil {
		t.Fatalf("AddPushSub: %v", err)
	}
	// Same endpoint upserts.
	sub.Auth = "rotated"
	if err := s.Relay.AddPushSub(sub); err != nil {
		t.Fatalf("AddPushSub upsert: %v", err)
	}

	subs, err := s.Relay.PushSubs(daemon)
	if err != nil {
		t.Fatalf("PushSubs: %v", err)
	}
	if len(subs) != 1 || subs[0].Auth != "rotated" || subs[0].DeviceID != d.ID {
		t.Fatalf("unexpected subs: %+v", subs)
	}

	// Deleting the device removes its subscriptions.
	if err := s.Relay.DeleteDevice(d.ID); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if subs, _ := s.Relay.PushSubs(daemon); len(subs) != 0 {
		t.Fatalf("subs should be gone with the device: %+v", subs)
	}
}
