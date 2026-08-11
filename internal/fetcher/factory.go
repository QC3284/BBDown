package fetcher

import (
	"strings"

	"github.com/QC3284/BBDown/internal/util"
)

// Factory creates the appropriate Fetcher based on the aid prefix.
type Factory struct {
	HTTPClient *util.HTTPClient
	UseIntlAPI bool
}

// NewFactory creates a new FetcherFactory.
func NewFactory(client *util.HTTPClient, useIntlAPI bool) *Factory {
	return &Factory{
		HTTPClient: client,
		UseIntlAPI: useIntlAPI,
	}
}

// Create returns the appropriate fetcher for the given aid prefix.
func (f *Factory) Create(aidOri string) Fetcher {
	switch {
	case strings.HasPrefix(aidOri, "cheese"):
		return &CheeseInfoFetcher{client: f.HTTPClient}
	case strings.HasPrefix(aidOri, "ep"):
		if f.UseIntlAPI {
			return &IntlBangumiInfoFetcher{client: f.HTTPClient}
		}
		return &BangumiInfoFetcher{client: f.HTTPClient}
	case strings.HasPrefix(aidOri, "mid"):
		return &SpaceVideoFetcher{client: f.HTTPClient}
	case strings.HasPrefix(aidOri, "listBizId"):
		return &MediaListFetcher{client: f.HTTPClient}
	case strings.HasPrefix(aidOri, "seriesBizId"):
		return &SeriesListFetcher{client: f.HTTPClient}
	case strings.HasPrefix(aidOri, "favId"):
		return &FavListFetcher{client: f.HTTPClient}
	default:
		return &NormalInfoFetcher{client: f.HTTPClient}
	}
}
