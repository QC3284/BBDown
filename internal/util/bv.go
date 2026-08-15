package util

import (
	"fmt"

	"github.com/QC3284/BBDown/internal/entity"
)

// BilibiliBvConverter converts between AV (aid) and BV strings.
// Delegates to the canonical implementation in internal/entity to avoid duplication.
// Based on: https://github.com/Colerar/abv/blob/main/src/lib.rs

// AvToBv converts an AV number (aid) to a BV string.
func AvToBv(aid int64) (string, error) {
	if aid < 1 {
		return "", fmt.Errorf("av %d is smaller than 1", aid)
	}
	return entity.AvToBv(aid)
}

// BvToAv converts a BV string to an AV number (aid).
// The input should be the 9-char suffix after "BV1".
func BvToAv(bvSuffix string) (int64, error) {
	return entity.BvToAv(bvSuffix)
}

// AvToBvStr converts aid string to BV string (never fails; non-numeric input passes through).
func AvToBvStr(aidStr string) (string, error) {
	return entity.AvToBvStr(aidStr), nil
}
