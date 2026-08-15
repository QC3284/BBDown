package entity

import (
	"math/rand"
	"testing"
)

// BV17x411w7KC <-> aid 170001 is a real-world pair verified against the Bilibili API.
func TestAvToBvKnownVector(t *testing.T) {
	bv, err := AvToBv(170001)
	if err != nil {
		t.Fatalf("AvToBv(170001): %v", err)
	}
	if bv != "BV17x411w7KC" {
		t.Fatalf("AvToBv(170001) = %q, want BV17x411w7KC", bv)
	}
	aid, err := BvToAv("7x411w7KC")
	if err != nil {
		t.Fatalf("BvToAv: %v", err)
	}
	if aid != 170001 {
		t.Fatalf("BvToAv(17x411w7KC) = %d, want 170001", aid)
	}
}

func TestBvRoundTrip(t *testing.T) {
	// Deterministic and pseudo-random round trips across the aid space.
	aids := []int64{1, 2, 58, 170001, 1 << 20, 1 << 30, 1<<40 - 1, (1 << 51) - 1}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		aids = append(aids, rng.Int63n((1<<51)-2)+1)
	}
	for _, aid := range aids {
		bv, err := AvToBv(aid)
		if err != nil {
			t.Fatalf("AvToBv(%d): %v", aid, err)
		}
		if len(bv) != 12 || bv[:3] != "BV1" {
			t.Fatalf("AvToBv(%d) = %q, malformed BV", aid, bv)
		}
		back, err := BvToAv(bv[3:])
		if err != nil {
			t.Fatalf("BvToAv(%q): %v", bv[3:], err)
		}
		if back != aid {
			t.Fatalf("round trip failed: %d -> %q -> %d", aid, bv, back)
		}
	}
}

func TestBvToAvErrors(t *testing.T) {
	if _, err := BvToAv("short"); err == nil {
		t.Fatal("expected error for short suffix")
	}
	if _, err := BvToAv("!!!!11111"); err == nil {
		t.Fatal("expected error for invalid chars")
	}
	if _, err := AvToBv(0); err == nil {
		t.Fatal("expected error for aid 0")
	}
	if _, err := AvToBv(1 << 51); err == nil {
		t.Fatal("expected error for out-of-range aid")
	}
}

func TestAvToBvStr(t *testing.T) {
	if got := AvToBvStr("170001"); got != "BV17x411w7KC" {
		t.Fatalf("AvToBvStr(170001) = %q", got)
	}
	if got := AvToBvStr("not-a-number"); got != "not-a-number" {
		t.Fatalf("AvToBvStr(non-numeric) = %q", got)
	}
}

func TestPageBvid(t *testing.T) {
	p := Page{Aid: "170001"}
	if p.Bvid() != "BV17x411w7KC" {
		t.Fatalf("Page.Bvid() = %q", p.Bvid())
	}
	p2 := Page{Aid: "bogus"}
	if p2.Bvid() != "bogus" {
		t.Fatalf("Page.Bvid() for non-numeric = %q", p2.Bvid())
	}
}
