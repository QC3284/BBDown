package appapi

import (
	"testing"
)

// TestEncodePlayViewReqRoundTrip encodes a request and walks it back to
// verify the field numbers/values match the upstream playviewreq.proto.
func TestEncodePlayViewReqRoundTrip(t *testing.T) {
	b := encodePlayViewReq(170001, 279786, 0, code265)

	got := make(map[int]uint64)
	var spmid, fromSpmid string
	walkFields(b, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		switch wireType {
		case 0:
			got[fieldNum] = varint
		case 2:
			if fieldNum == 9 {
				spmid = string(val)
			}
			if fieldNum == 10 {
				fromSpmid = string(val)
			}
		}
		return true
	})

	if got[1] != 170001 {
		t.Errorf("field 1 (epId/aid) = %d, want 170001", got[1])
	}
	if got[2] != 279786 {
		t.Errorf("field 2 (cid) = %d, want 279786", got[2])
	}
	if got[3] != 127 {
		t.Errorf("field 3 (qn, 0 => 127) = %d", got[3])
	}
	if got[5] != 4048 {
		t.Errorf("field 5 (fnval) = %d, want 4048", got[5])
	}
	if got[7] != 2 {
		t.Errorf("field 7 (forceHost) = %d, want 2", got[7])
	}
	if got[8] != 1 {
		t.Errorf("field 8 (fourk) = %d, want 1", got[8])
	}
	if got[12] != 2 {
		t.Errorf("field 12 (preferCodecType) = %d, want 2 (HEVC)", got[12])
	}
	if spmid != "main.ugc-video-detail.0.0" || fromSpmid != "main.my-history.0.0" {
		t.Errorf("spmid/fromSpmid = %q/%q", spmid, fromSpmid)
	}
}

func TestPackReadMessageRoundTrip(t *testing.T) {
	payload := []byte("hello playview")
	packed := packMessage(payload)
	if packed[0] != 1 {
		t.Fatal("packed flag byte should be 1 (gzip)")
	}
	got, err := readMessage(packed)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("round trip = %q", got)
	}
}

func TestGetVideoCodeType(t *testing.T) {
	cases := map[string]int{
		"AVC":  code264,
		"HEVC": code265,
		"AV1":  codeAV1,
		"":     code265,
	}
	for in, want := range cases {
		if got := getVideoCodeType(in); got != want {
			t.Errorf("getVideoCodeType(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestWalkFieldsRejectsMalformedInput guards the protobuf walker against
// attacker-controlled gRPC frames. A length-delimited field whose varint
// length exceeds MaxInt64 used to wrap negative in the int conversion, slip
// past the bounds check and slice out of range.
func TestWalkFieldsRejectsMalformedInput(t *testing.T) {
	noop := func(fieldNum, wireType int, val []byte, v uint64) bool { return true }

	// field 1, wire type 2, length = 0xFFFFFFFFFFFFFFFF
	overflowing := []byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	called := 0
	walkFields(overflowing, func(fieldNum, wireType int, val []byte, v uint64) bool {
		called++
		return true
	})
	if called != 0 {
		t.Errorf("overflowing length: fn called %d times, want 0", called)
	}

	// A varint field with no terminating byte must return, not spin forever.
	truncated := 0
	walkFields([]byte{0x08, 0x80, 0x80}, func(fieldNum, wireType int, val []byte, v uint64) bool {
		truncated++
		return true
	})
	if truncated != 0 {
		t.Errorf("truncated varint: fn called %d times, want 0", truncated)
	}

	// A well-formed field must still be delivered.
	ok := 0
	walkFields([]byte{0x0a, 0x02, 0xaa, 0xbb}, func(fieldNum, wireType int, val []byte, v uint64) bool {
		ok++
		if fieldNum != 1 || wireType != 2 || len(val) != 2 {
			t.Errorf("unexpected field: num=%d wire=%d val=%v", fieldNum, wireType, val)
		}
		return true
	})
	if ok != 1 {
		t.Errorf("well-formed field: fn called %d times, want 1", ok)
	}
	_ = noop
}
