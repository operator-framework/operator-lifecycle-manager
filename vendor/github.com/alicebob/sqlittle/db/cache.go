// table and index page cache

package db

import (
	"sync"
)

type btreeCache struct {
	limit int
	elem  map[int]interface{}
	mu    sync.RWMutex
}

func newBtreeCache(limit int) *btreeCache {
	return &btreeCache{
		limit: limit,
		elem:  make(map[int]interface{}, limit),
	}
}

func (t *btreeCache) get(p int) interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.elem[p]
}

func (t *btreeCache) set(p int, btree interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.elem) >= t.limit {
		// cache full? Simply drop the whole thing.
		t.elem = make(map[int]interface{}, t.limit)
	}
	t.elem[p] = btree
}

func (t *btreeCache) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.elem = make(map[int]interface{}, t.limit)
}
