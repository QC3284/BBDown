package download

import "github.com/QC3284/BBDown-go/internal/util"

// newTestClient builds an HTTP client for the download tests.
func newTestClient() *util.HTTPClient {
	return util.NewHTTPClient(nil, func() string { return "" }, nil)
}
