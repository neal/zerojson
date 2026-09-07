// Package zerojson is a fixture whose PACKAGE NAME deliberately collides
// with the runtime's own. The generated file imports the runtime under a
// fixed qualifier, so a dependency package of that name has to be aliased
// instead of claiming it (a duplicate import otherwise fails to compile).
package zerojson

import zj "github.com/neal/zerojson"

// Token is a leaf type encoded as a JSON string.
type Token string

// Append implements zerojsongen's custom leaf append contract.
func Append(dst []byte, t *Token) []byte { return zj.AppendString(dst, string(*t)) }

// Read implements zerojsongen's custom leaf read contract.
func Read(l *zj.Lexer, t *Token) { *t = Token(l.ReadString()) }
