package zerojson

import "sync"

// internMaxLen bounds what we intern; anything longer is almost certainly a
// unique ID, not a repeated token.
const internMaxLen = 24

// internShardMax caps entries per shard so unexpectedly high-cardinality
// inputs degrade to plain copies instead of growing the table without bound.
const internShardMax = 4096

// The intern table is sharded to reduce contention between concurrent
// decoders. Its fixed maximum is internShards*internShardMax entries.
const internShards = 32

type internShard struct {
	sync.RWMutex
	m map[string]string
	_ [24]byte // pad to a cache line to avoid false sharing of locks
}

var internTable = func() *[internShards]internShard {
	var t [internShards]internShard
	for i := range t {
		t[i].m = make(map[string]string, 64)
	}
	return &t
}()

// internHash is FNV-1a over the token bytes.
func internHash(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h = (h ^ uint32(c)) * 16777619
	}
	return h
}

// Intern returns a canonical string for b, allocating only the first time a
// short value is seen while its shard has capacity. Once a shard reaches its
// fixed cap, new values are copied but not retained. Lookups for retained
// values are allocation-free.
func Intern(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) > internMaxLen {
		return string(b)
	}
	sh := &internTable[internHash(b)%internShards]
	sh.RLock()
	s, ok := sh.m[string(b)]
	sh.RUnlock()
	if ok {
		return s
	}
	sh.Lock()
	s, ok = sh.m[string(b)]
	if !ok {
		s = string(b)
		if len(sh.m) < internShardMax {
			sh.m[s] = s
		}
	}
	sh.Unlock()
	return s
}
