package fetcher

import (
	"context"

	"github.com/QC3284/BBDown/internal/entity"
)

// Fetcher parses video information from Bilibili.
type Fetcher interface {
	Fetch(ctx context.Context, id string) (*entity.VInfo, error)
}
