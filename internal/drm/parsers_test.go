package drm

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadWvdDeviceRejectsMalformedInput guards the panic sites found by the
// DRM audit: a zero-byte .wvd file reached data[0] directly, and the v1 parser
// sliced with lengths read verbatim from the file. Malformed input from disk
// (or a user-supplied device file) must produce an error, never a panic.
func TestLoadWvdDeviceRejectsMalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"empty file":             {},
		"single version byte":    {1},
		"v1 header only":         {1, 0, 0, 0, 0, 0},
		"v1 overlong key length": {1, 0, 0, 0, 0xff, 0xff},
		"v1 truncated client id": {1, 0, 0, 0, 0x00, 0x02, 0xaa, 0xbb},
		"wvd magic, no payload":  {'W', 'V', 'D', 1},
		"unknown format":         {0x7f, 0x7f, 0x7f},
	}
	dir := t.TempDir()
	for name, data := range cases {
		p := filepath.Join(dir, "device.wvd")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWvdDevice(p); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

// TestLicenseParsersRejectMalformedInput covers the license protobuf parsers.
// A huge varint length used to wrap negative in the int conversion and slice
// out of range; an unhandled wire type left the cursor unadvanced and spun
// forever. Both must now terminate cleanly with no keys.
func TestLicenseParsersRejectMalformedInput(t *testing.T) {
	// field 3, wire type 2, length = 0xFFFFFFFFFFFFFFFF
	overflowing := []byte{0x1a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if keys := parseLicenseKeys(overflowing); len(keys) != 0 {
		t.Errorf("parseLicenseKeys: got %d keys, want 0", len(keys))
	}
	if kc := parseKeyContainer(overflowing); kc == nil {
		t.Error("parseKeyContainer: got nil, want a container")
	}
	if _, _, _, _, _ = parseSignedMessage(overflowing); false {
		t.Error("unreachable")
	}

	// wire type 3 (group start) has no handler: the loop must break.
	for _, data := range [][]byte{{0x0b}, {0x1b}, {0x0d}} {
		_ = parseLicenseKeys(data)
		_ = parseKeyContainer(data)
		_, _, _, _, _ = parseSignedMessage(data)
	}

	// Length-delimited field whose varint never terminates inside the buffer.
	for _, data := range [][]byte{{0x0a, 0x80, 0x80}, {0x1a, 0x80, 0x80}} {
		_ = parseLicenseKeys(data)
		_ = parseKeyContainer(data)
		_, _, _, _, _ = parseSignedMessage(data)
	}

	// Varint field with no terminating byte.
	_, _, _, _, _ = parseSignedMessage([]byte{0x08, 0x80, 0x80})
	_ = parseKeyContainer([]byte{0x20, 0x80, 0x80})
}
