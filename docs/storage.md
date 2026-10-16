# File storage

An app keeps files (uploads, generated files, anything too big for a
row) in one bucket, `app.Storage()`, a `storage.Bucket`. There are
three, and they behave the same, as one conformance suite checks:

| Bucket | For |
|---|---|
| `storage.S3(storage.S3Config{...})` | production: AWS S3, Cloudflare R2, MinIO, Backblaze B2, Tigris, any S3-compatible service |
| `storage.Dir(path)` | one server, or dev |
| `storage.Memory()` | tests |

## Configure

```go
app := loom.New(loom.Config{
	DB: os.Getenv("DATABASE_URL"),
	Storage: storage.S3(storage.S3Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"), // "" for AWS S3; "https://<account>.r2.cloudflarestorage.com"
		Region:          os.Getenv("S3_REGION"),   // default "us-east-1"; R2: "auto"
		Bucket:          os.Getenv("S3_BUCKET"),   // must exist: Loom never creates buckets
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
	}),
})
```

`app.Run` checks the bucket before it serves, so a wrong secret
(SG004), a missing bucket (SG005), an unreachable endpoint (SG003) or
an incomplete config (SG002) stops the deploy rather than the first
upload. `loomtest.Mount` runs the same check.

With no `Storage`, `app.Storage()` is a directory, `.loom/storage`,
under `loom dev` (files last across rebuilds), memory in other dev
mode (`Config.Dev: loom.DevOn`), and outside dev a bucket whose every
call fails with SG001, so a missing setting is loud.

The credentials need to read, write, delete and list objects in the
bucket. Objects are addressed path-style
(`https://endpoint/bucket/key`), which every S3-compatible service
supports, and requests are signed with AWS Signature Version 4.

## Use

```go
b := app.Storage()

err := b.Put(ctx, "avatars/42.png", r, size, storage.PutOptions{
	ContentType:  "image/png",                      // default application/octet-stream
	CacheControl: "public, max-age=31536000, immutable",
})
body, obj, err := b.Get(ctx, "avatars/42.png") // close body; storage.ErrNotFound if none
obj, err := b.Stat(ctx, "avatars/42.png")      // Size, ContentType, CacheControl, ETag, Modified
err = b.Delete(ctx, "avatars/42.png")          // no error if it's gone
objs, err := b.List(ctx, "avatars/", after, 100) // key order; pass the last key as after for the next page
```

`Put` takes the body's exact size (SG008 if it differs): for bytes,
`bytes.NewReader(data), int64(len(data))`. The body streams; it isn't
read into memory on S3 or disk.

Keys are paths of plain segments: no leading, trailing or doubled `/`,
no `.` or `..` segment, no backslash or control character, at most
1024 bytes (SG006, `storage.ValidKey`). Don't make a key that is also
another key's directory (`a` and `a/b`): S3 and disk allow it, MinIO
doesn't.

## Serve

```go
app.Handle("GET /avatars/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	storage.Serve(w, r, app.Storage(), "avatars/"+r.PathValue("id"))
}))
```

`Serve` streams the object with its Content-Type, Content-Length,
ETag, Last-Modified and Cache-Control (unless the handler set one
first), answers a matching `If-None-Match` with 304, and a missing
object or bad key with 404. The bucket stays private; put a CDN in
front of the app to cache files.

## Files and transactions

A bucket isn't part of a database transaction. Write the file first,
then the row that names it:

```go
hash, err := putFile(ctx, data) // key from the content's hash
if err != nil {
	return err // the job retries
}
return db.Tx(ctx, func(tx loom.Tx) error {
	_, err := tx.Exec(ctx, `update photos set file = $1 where id = $2`, hash, id)
	return err
})
```

A rolled-back transaction then leaves an unused file, never a row
naming a missing one. Content-addressed keys (the SHA-256 of the
contents) make a retried job's second write harmless and store
duplicates once.

## See it

`loom storage [PREFIX] [--after KEY] [--limit N] [--json]` lists the
bucket of an app running in dev; `storage_list` is the same in
`loom mcp`.

## Test

Give tests `Storage: storage.Memory()`. A custom `Bucket` (another
cloud's API, say) passes `storagetest.Run`, the suite every bucket
runs:

```go
func TestBucket(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Bucket { return newEmptyBucket(t) })
}
```

Loom's own S3 tests run against MinIO when `LOOM_TEST_S3` is set, e.g.
`http://minioadmin:minioadmin@localhost:9000/loom-test`.
