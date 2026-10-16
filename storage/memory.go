package storage

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
)

// Memory returns an empty bucket kept in memory, for tests. Its objects
// last as long as the value.
func Memory() Bucket { return &memory{objects: map[string]memObject{}} }

type memory struct {
	mu      sync.Mutex
	objects map[string]memObject
}

type memObject struct {
	data []byte
	obj  Object
}

// newObject is the details of data put at key with o, now.
func newObject(key string, data []byte, o PutOptions) Object {
	sum := md5.Sum(data)
	return Object{
		Key:          key,
		Size:         int64(len(data)),
		ContentType:  cmp.Or(o.ContentType, DefaultContentType),
		CacheControl: o.CacheControl,
		ETag:         `"` + hex.EncodeToString(sum[:]) + `"`,
		Modified:     time.Now().UTC().Truncate(time.Second),
	}
}

func (m *memory) Put(ctx context.Context, key string, body io.Reader, size int64, o PutOptions) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	data, err := readExactly(key, body, size)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObject{data: data, obj: newObject(key, data, o)}
	return nil
}

func (m *memory) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	if err := ValidKey(key); err != nil {
		return nil, Object{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, Object{}, notFound(key)
	}
	return io.NopCloser(bytes.NewReader(o.data)), o.obj, nil
}

func (m *memory) Stat(ctx context.Context, key string) (Object, error) {
	if err := ValidKey(key); err != nil {
		return Object{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return Object{}, notFound(key)
	}
	return o.obj, nil
}

func (m *memory) Delete(ctx context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *memory) List(ctx context.Context, prefix, after string, limit int) ([]Object, error) {
	if err := validPrefix(prefix); err != nil {
		return nil, err
	}
	m.mu.Lock()
	var keys []string
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	keys = keys[:min(len(keys), listLimit(limit))]
	out := make([]Object, len(keys))
	for i, k := range keys {
		out[i] = listed(m.objects[k].obj)
	}
	m.mu.Unlock()
	return out, nil
}

func (m *memory) Check(ctx context.Context) error { return nil }
func (m *memory) Describe() string                { return "memory" }

// listed is o as List gives it: without the HTTP details.
func listed(o Object) Object {
	o.ContentType, o.CacheControl = "", ""
	return o
}
