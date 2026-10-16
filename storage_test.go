package loom_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/storage"
)

// Storage: Config.Storage is used; without it, dev tools get memory and
// production fails loudly (SG001) on use, not at startup.
func TestStorage(t *testing.T) {
	ctx := context.Background()
	b := storage.Memory()
	if loom.New(loom.Config{Storage: b}).Storage() != b {
		t.Error("app.Storage() isn't Config.Storage")
	}
	var e *storage.Error
	prod := quietApp()
	if err := prod.Validate(ctx); err != nil {
		t.Errorf("an app with no storage fails Validate: %v", err)
	}
	if err := prod.Storage().Put(ctx, "k", strings.NewReader("x"), 1, storage.PutOptions{}); !errors.As(err, &e) || e.Code != storage.SG001 {
		t.Errorf("Put with no storage outside dev = %v, want SG001", err)
	}
	dev := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: loom.DevOn})
	if err := dev.Storage().Put(ctx, "k", strings.NewReader("x"), 1, storage.PutOptions{}); err != nil {
		t.Errorf("Put in dev with no storage = %v", err)
	}
	if dev.Storage().Describe() != "memory" || dev.Storage() != dev.Storage() {
		t.Errorf("dev storage is %s, or not the same bucket each time", dev.Storage().Describe())
	}
}

// Validate (so Run and loomtest.Mount) checks the bucket.
func TestStorageCheck(t *testing.T) {
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Storage: storage.S3(storage.S3Config{Endpoint: "http://127.0.0.1:1"})})
	var e *storage.Error
	if err := app.Validate(context.Background()); !errors.As(err, &e) || e.Code != storage.SG002 {
		t.Errorf("Validate with an incomplete S3Config = %v, want SG002", err)
	}
	app = loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Storage: storage.S3(storage.S3Config{Endpoint: "http://127.0.0.1:1", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"})})
	if err := app.Validate(context.Background()); !errors.As(err, &e) || e.Code != storage.SG003 {
		t.Errorf("Validate with an unreachable bucket = %v, want SG003", err)
	}
}

// /_loom/dev/storage lists the bucket for `loom storage`.
func TestDevStorage(t *testing.T) {
	ctx := context.Background()
	b := storage.Memory()
	for _, k := range []string{"a/1", "a/2", "b/1"} {
		b.Put(ctx, k, strings.NewReader(k), int64(len(k)), storage.PutOptions{})
	}
	app := loom.New(loom.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Dev: loom.DevOn, Storage: b})
	srv := httptest.NewServer(app)
	defer srv.Close()
	list := func(q string) loom.StorageList {
		res, err := http.Get(srv.URL + "/_loom/dev/storage" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var l loom.StorageList
		if err := json.NewDecoder(res.Body).Decode(&l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	keys := func(l loom.StorageList) string {
		var ks []string
		for _, o := range l.Objects {
			ks = append(ks, o.Key)
		}
		return strings.Join(ks, " ")
	}
	if l := list(""); l.Bucket != "memory" || keys(l) != "a/1 a/2 b/1" || l.Objects[0].Size != 3 {
		t.Errorf("list = %+v", l)
	}
	if l := list("?prefix=a/&after=a/1"); keys(l) != "a/2" {
		t.Errorf("list a/ after a/1 = %q", keys(l))
	}
	if l := list("?limit=1"); keys(l) != "a/1" {
		t.Errorf("list limit 1 = %q", keys(l))
	}
	if l := list("?prefix=zzz"); l.Objects == nil {
		t.Error("an empty list is null, want []")
	}
	res, _ := http.Get(srv.URL + "/_loom/dev/storage?prefix=/bad")
	var e map[string]string
	json.NewDecoder(res.Body).Decode(&e)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || e["code"] != storage.SG006 || e["fix"] == "" {
		t.Errorf("a bad prefix = %d %v, want SG006", res.StatusCode, e)
	}
}
