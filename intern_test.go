package zerojson

import (
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
