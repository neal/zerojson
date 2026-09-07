// Package atomic deliberately collides with sync/atomic, which pooled
// decoders import for their concurrent-use guard. An import alias in the
// source fixture is insufficient: the generator uses the declared name.
package atomic

import "github.com/neal/zerojson"

type Token string

func Append(dst []byte, t *Token) []byte { return zerojson.AppendString(dst, string(*t)) }

func Read(l *zerojson.Lexer, t *Token) { *t = Token(l.ReadString()) }
