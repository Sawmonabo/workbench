package release

import (
	"context"

	"github.com/Sawmonabo/workbench/internal/download"
)

// Download fetches the HTTPS resource at location, at most limit bytes, as
// an octet stream, and names the server's answers as a release's (see
// [getRelease]); callers verify its content. progress, when set, is told the
// bytes received and the size the server sent (-1 when it sent none).
func Download(
	ctx context.Context,
	location string,
	limit int64,
	progress func(done, total int64),
) ([]byte, error) {
	return getRelease(
		ctx,
		location,
		limit,
		download.Request{Accept: "application/octet-stream", Progress: progress},
	)
}
