package util

import (
	"fmt"
)

// BilibiliBvConverter converts between AV (aid) and BV strings.
// Based on: https://github.com/Colerar/abv/blob/main/src/lib.rs

const (
	xorCode  int64 = 23442827791579
	maskCode int64 = (1 << 51) - 1
	maxAid   int64 = maskCode + 1
	minAid   int64 = 1
	bvBase   int64 = 58
	bvLen          = 9
)

var alphabet = []byte("FcwAPNKTMug3GV5Lj7EJnHpWsx4tb8haYeviqBz6rkCy12mUSDQX9RdoZf")

var revAlphabet map[byte]int64

func init() {
	revAlphabet = make(map[byte]int64, len(alphabet))
	for i, b := range alphabet {
		revAlphabet[b] = int64(i)
	}
}

// AvToBv converts an AV number (aid) to a BV string.
func AvToBv(aid int64) (string, error) {
	if aid < minAid {
		return "", fmt.Errorf("av %d is smaller than %d", aid, minAid)
	}
	if aid >= maxAid {
		return "", fmt.Errorf("av %d is bigger than %d", aid, maxAid)
	}

	bvid := make([]byte, bvLen)
	tmp := (maxAid | aid) ^ xorCode

	for i := bvLen - 1; i >= 0 && tmp != 0; i-- {
		bvid[i] = alphabet[tmp%bvBase]
		tmp /= bvBase
	}

	// Swap positions
	bvid[0], bvid[6] = bvid[6], bvid[0]
	bvid[1], bvid[4] = bvid[4], bvid[1]

	return "BV1" + string(bvid), nil
}

// BvToAv converts a BV string to an AV number (aid).
// The input should be the 9-char suffix after "BV1".
func BvToAv(bvSuffix string) (int64, error) {
	if len(bvSuffix) != bvLen {
		return 0, fmt.Errorf("BV suffix must be %d chars, got %d", bvLen, len(bvSuffix))
	}

	bvid := []byte(bvSuffix)
	bvid[0], bvid[6] = bvid[6], bvid[0]
	bvid[1], bvid[4] = bvid[4], bvid[1]

	var aid int64
	for _, b := range bvid {
		idx, ok := revAlphabet[b]
		if !ok {
			return 0, fmt.Errorf("invalid BV character: %c", b)
		}
		aid = aid*bvBase + idx
	}

	return (aid & maskCode) ^ xorCode, nil
}

// AvToBvStr converts aid string to BV string.
func AvToBvStr(aidStr string) (string, error) {
	var aid int64
	if _, err := fmt.Sscanf(aidStr, "%d", &aid); err != nil {
		return aidStr, nil // fallback: return as-is if not numeric
	}
	bv, err := AvToBv(aid)
	if err != nil {
		return aidStr, nil
	}
	return bv, nil
}
