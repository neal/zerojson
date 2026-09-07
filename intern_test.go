package zerojson

import (
	"fmt"
	"testing"
)

func TestReadInterfaceDoesNotInternObjectKeys(t *testing.T) {
	const key = "zj-interface-probe"
	sh := &internTable[internHash([]byte(key))%internShards]

	// Make the assertion independent of any earlier test that happened to use
	// the same value, then restore the table afterward.
	sh.Lock()
	previous, existed := sh.m[key]
	delete(sh.m, key)
	sh.Unlock()
	t.Cleanup(func() {
		sh.Lock()
		if existed {
			sh.m[key] = previous
		} else {
			delete(sh.m, key)
		}
		sh.Unlock()
	})

	l := Lexer{data: []byte(`{"zj-interface-probe":1}`)}
	_ = l.ReadInterface()
	l.Finish()
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	sh.RLock()
	_, interned := sh.m[key]
	sh.RUnlock()
	if interned {
		t.Fatal("ReadInterface retained an arbitrary object key in the intern table")
	}
}

// TestInternGenerations pins that a full shard starts a new generation
// instead of freezing: after a high-cardinality burst, a fresh value is
// still interned (a second lookup returns the retained string).
func TestInternGenerations(t *testing.T) {
	for i := 0; i < internShards*internShardMax*2; i++ {
		Intern([]byte(fmt.Sprintf("burst-%d", i)))
	}
	a := Intern([]byte("hot-after-burst"))
	b := Intern([]byte("hot-after-burst"))
	sh := &internTable[internHash([]byte("hot-after-burst"))%internShards]
	sh.RLock()
	_, retained := sh.m["hot-after-burst"]
	sh.RUnlock()
	if a != b || !retained {
		t.Fatal("value first seen after a burst was not retained by the intern table")
	}
	for i := range internTable {
		sh := &internTable[i]
		sh.RLock()
		n := len(sh.m)
		sh.RUnlock()
		if n > internShardMax {
			t.Fatalf("shard grew past its cap: %d", n)
		}
	}
}
