package storage

import (
	"context"
	"net/http"
	"time"
)

// CreateBucket makes the bucket cfg names, if it doesn't exist, for the
// S3 tests: Loom itself never creates buckets. It retries for 30
// seconds while the service starts.
func CreateBucket(cfg S3Config) error {
	b := S3(cfg).(*s3)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		res, err := b.do(ctx, "PUT", b.objectURL("", nil), nil, nil, 0, emptyHash)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusConflict { // made, or there already
				return nil
			}
			return b.responseError(res, "create", "")
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Second):
		}
	}
}
