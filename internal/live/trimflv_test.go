package live

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildFLV assembles a container with the given tag payload sizes.
func buildFLV(tagSizes ...int) []byte {
	out := []byte{'F', 'L', 'V', 1, 0, 0, 0, 0, 9}
	out = append(out, 0, 0, 0, 0) // PreviousTagSize0
	for _, size := range tagSizes {
		tag := make([]byte, 11)
		tag[0] = 9 // tag type: video
		tag[1] = byte(size >> 16)
		tag[2] = byte(size >> 8)
		tag[3] = byte(size)
		out = append(out, tag...)
		out = append(out, make([]byte, size)...)
		prev := make([]byte, 4)
		binary.BigEndian.PutUint32(prev, uint32(11+size))
		out = append(out, prev...)
	}
	return out
}

func TestTrimFLVTailDropsIncompleteTag(t *testing.T) {
	dir := t.TempDir()
	full := buildFLV(10, 20)

	// A clean file is left untouched.
	clean := filepath.Join(dir, "clean.flv")
	if err := os.WriteFile(clean, full, 0o644); err != nil {
		t.Fatal(err)
	}
	if dropped, err := trimFLVTail(clean); err != nil || dropped != 0 {
		t.Errorf("clean segment: dropped=%d err=%v, want 0/nil", dropped, err)
	}

	// Half a tag at the end must be cut back to the last complete one.
	truncated := filepath.Join(dir, "truncated.flv")
	halfTag := []byte{9, 0, 0, 100, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3} // header claiming 100 bytes, 3 provided
	if err := os.WriteFile(truncated, append(append([]byte{}, full...), halfTag...), 0o644); err != nil {
		t.Fatal(err)
	}
	dropped, err := trimFLVTail(truncated)
	if err != nil {
		t.Fatalf("trimFLVTail: %v", err)
	}
	if dropped != int64(len(halfTag)) {
		t.Errorf("dropped = %d, want %d", dropped, len(halfTag))
	}
	got, err := os.ReadFile(truncated)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(full) {
		t.Errorf("trimmed size = %d, want %d", len(got), len(full))
	}

	// Non-FLV input is left alone rather than mangled.
	other := filepath.Join(dir, "other.bin")
	if err := os.WriteFile(other, []byte("not an flv file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dropped, err := trimFLVTail(other); err != nil || dropped != 0 {
		t.Errorf("non-FLV: dropped=%d err=%v, want 0/nil", dropped, err)
	}
}
