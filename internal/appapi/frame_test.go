package appapi

import "testing"

// TestReadMessageValidatesFrameHeader: the first byte of a gRPC frame is the
// compressed flag (0 or 1). Any other value is a malformed frame and used to be
// passed to the protobuf decoder as payload, producing a confusing error far
// from the cause.
func TestReadMessageValidatesFrameHeader(t *testing.T) {
	if _, err := readMessage([]byte{7, 0, 0, 0, 4, 1, 2, 3, 4}); err == nil {
		t.Error("an illegal frame flag must be rejected")
	}
	if _, err := readMessage([]byte{0, 0}); err == nil {
		t.Error("a buffer shorter than the frame header must be rejected")
	}

	got, err := readMessage([]byte{0, 0, 0, 0, 2, 0xAB, 0xCD})
	if err != nil {
		t.Fatalf("uncompressed frame: %v", err)
	}
	if len(got) != 2 || got[0] != 0xAB || got[1] != 0xCD {
		t.Errorf("payload = %v, want AB CD", got)
	}
}
