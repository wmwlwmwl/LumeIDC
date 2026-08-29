package zjmf

import (
	"context"
	"net/http"
)

func httpNewRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, rawURL, nil)
}
