package loom

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/moomerman/loom/storage"
)

// File storage (docs/storage.md). An app keeps files in the bucket it
// gives in Config.Storage: storage.S3 for any S3-compatible service,
// storage.Dir for a directory, storage.Memory in tests. Run checks the
// bucket before serving (SG002 to SG005), so wrong credentials or a
// missing bucket stop the deploy, not the first upload.
//
//	err := app.Storage().Put(ctx, key, r, size, storage.PutOptions{ContentType: "image/png"})
//	storage.Serve(w, r, app.Storage(), key)

// DevStorageDir is where an app under `loom dev` with no Config.Storage
// keeps its files, relative to the directory it runs in, so they last
// across rebuilds.
const DevStorageDir = ".loom/storage"

// Storage returns the app's bucket: Config.Storage, or without one, a
// directory (DevStorageDir) under `loom dev`, memory in other dev mode
// (tests and tools, Config.Dev: DevOn), and outside dev a bucket whose
// every call fails with SG001.
func (a *App) Storage() storage.Bucket {
	a.storageOnce.Do(func() {
		switch {
		case a.cfg.Storage != nil:
			a.storage = a.cfg.Storage
		case a.devEnv:
			a.storage = storage.Dir(filepath.FromSlash(DevStorageDir))
		case a.dev:
			a.storage = storage.Memory()
		default:
			a.storage = storage.Missing()
		}
	})
	return a.storage
}

// checkStorage checks, for Run and Validate, that the bucket can be
// used, within ten seconds.
func (a *App) checkStorage(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return a.Storage().Check(ctx)
}

// StorageList is `loom storage --json`: the bucket and a page of its
// objects.
type StorageList struct {
	Bucket  string           `json:"bucket"` // where it is, e.g. "s3 https://host/bucket", "dir .loom/storage"
	Objects []storage.Object `json:"objects"`
}

// serveStorage lists objects for `loom storage`: ?prefix=, ?after= and
// ?limit= (default 100).
func (a *App) serveStorage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	b := a.Storage()
	objs, err := b.List(r.Context(), q.Get("prefix"), q.Get("after"), limit)
	if err != nil {
		info := errorInfo("", err)
		status := http.StatusBadGateway
		if info.Code == storage.SG006 {
			status = http.StatusBadRequest // the prefix
		}
		writeError(w, status, &Error{Code: info.Code, Msg: info.Message, Fix: info.Fix})
		return
	}
	if objs == nil {
		objs = []storage.Object{}
	}
	writeJSON(w, StorageList{Bucket: b.Describe(), Objects: objs})
}
