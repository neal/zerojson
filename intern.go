package zerojson

import "sync"

// internMaxLen bounds what we intern; anything longer is almost certainly a
// unique ID, not a repeated token.
const internMaxLen = 24

// internShardMax caps entries per shard: a full shard starts a new
// generation (see Intern), so the table never grows past its fixed size
// and never freezes either.
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

// Intern returns a string equal to b. Hits in the retained table do not
// allocate; misses and values longer than 24 bytes are copied.
//
// The process-global table retains at most 32 shards of 4,096 entries each.
// A full shard resets to a new generation, so previously seen values can
// allocate again. Strings held by callers stay valid across resets; the cap
// bounds table entries, not all live strings or transient GC memory.
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
		if len(sh.m) >= internShardMax {
			// New generation: drop the shard's references (not the
			// strings themselves) and start over with this value.
			clear(sh.m)
		}
		sh.m[s] = s
	}
	sh.Unlock()
	return s
}
