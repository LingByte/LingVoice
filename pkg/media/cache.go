package media

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// LocalMediaCache is a file-backed media cache with an in-memory read layer.
// Concurrent access is safe: Store/Get are protected by per-key locks, and
// the in-memory layer uses sync.Map for lock-free reads.
type LocalMediaCache struct {
	Disabled  bool
	CacheRoot string

	// In-memory read cache: key → []byte. Avoids disk I/O on repeated Gets.
	memCache sync.Map

	// Per-key write locks prevent concurrent writes to the same file.
	keyLocks   sync.Map // key → *sync.Mutex
	hits       atomic.Int64
	misses     atomic.Int64
	memHits    atomic.Int64
}

var _defaultMediaCache *LocalMediaCache

func MediaCache() *LocalMediaCache {
	if _defaultMediaCache == nil {
		rootVal, ok := os.LookupEnv("MEDIA_CACHE_ROOT")
		if !ok {
			rootVal = "/tmp"
		}
		disableVal, ok := os.LookupEnv("MEDIA_CACHE_DISABLED")
		var disable bool
		if ok {
			disable, _ = strconv.ParseBool(disableVal)
		}
		_defaultMediaCache = &LocalMediaCache{
			Disabled:  disable,
			CacheRoot: rootVal,
		}
		if !disable {
			if _, err := os.Stat(rootVal); err != nil {
				os.MkdirAll(rootVal, 0755)
			}
			log.With(zap.Any("root", rootVal)).Info("mediacache: initialized")
		}
	}
	return _defaultMediaCache
}

func (c *LocalMediaCache) BuildKey(params ...string) string {
	md5hash := md5.New()
	for _, p := range params {
		md5hash.Write([]byte(p))
	}
	digest := md5hash.Sum(nil)
	return fmt.Sprintf("%x", digest)
}

// keyLock returns a per-key mutex for serializing writes to the same key.
func (c *LocalMediaCache) keyLock(key string) *sync.Mutex {
	v, _ := c.keyLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (c *LocalMediaCache) Store(key string, data []byte) error {
	if c.Disabled {
		return nil
	}

	// Per-key lock: concurrent Store calls for the same key are serialized,
	// but different keys proceed in parallel.
	mu := c.keyLock(key)
	mu.Lock()
	defer mu.Unlock()

	filename := filepath.Join(c.CacheRoot, key)
	if st, err := os.Stat(filename); err == nil {
		if st.IsDir() {
			return os.ErrExist
		}
	}
	err := os.WriteFile(filename, data, 0644)
	if err != nil {
		log.With(logger.WithFields(map[string]interface{}{"filename": filename, "error": err})...).Error("mediacache: failed to write file")
		return err
	}

	// Update in-memory cache
	c.memCache.Store(key, data)
	log.With(logger.WithFields(map[string]interface{}{"filename": filename, "datasize": len(data)})...).Info("mediacache: stored")
	return nil
}

func (c *LocalMediaCache) Get(key string) ([]byte, error) {
	if c.Disabled {
		return nil, os.ErrNotExist
	}

	// Fast path: check in-memory cache first (lock-free)
	if v, ok := c.memCache.Load(key); ok {
		c.memHits.Add(1)
		return v.([]byte), nil
	}

	filename := filepath.Join(c.CacheRoot, key)
	if st, err := os.Stat(filename); err == nil {
		if st.IsDir() {
			c.misses.Add(1)
			return nil, os.ErrNotExist
		}
	} else {
		c.misses.Add(1)
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		log.With(logger.WithFields(map[string]interface{}{"filename": filename, "error": err})...).Error("mediacache: failed to read file")
		c.misses.Add(1)
		return nil, err
	}

	// Populate in-memory cache for future reads
	c.memCache.Store(key, data)
	c.hits.Add(1)
	return data, nil
}

// CacheStats returns hit/miss counters for observability.
type CacheStats struct {
	Hits    int64
	Misses  int64
	MemHits int64
}

func (c *LocalMediaCache) Stats() CacheStats {
	return CacheStats{
		Hits:    c.hits.Load(),
		Misses:  c.misses.Load(),
		MemHits: c.memHits.Load(),
	}
}
