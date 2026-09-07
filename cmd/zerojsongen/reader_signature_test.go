package main

import (
	"go/token"
	"go/types"
	"testing"
)

// TestReadZJSONSigLooksThroughAliases pins that ReadZJSON(*L) where L is
// an alias of zerojson.Lexer (or the parameter type is an alias of the
// pointer) is recognized as zerojson.Reader.
func TestReadZJSONSigLooksThroughAliases(t *testing.T) {
	rt := types.NewPackage(runtimePath, "zerojson")
	lexerObj := types.NewTypeName(token.NoPos, rt, "Lexer", nil)
	lexer := types.NewNamed(lexerObj, types.NewStruct(nil, nil), nil)
	user := types.NewPackage("example.com/user", "user")
	aliasObj := types.NewTypeName(token.NoPos, user, "LexerAlias", nil)
	alias := types.NewAlias(aliasObj, lexer)
	ptrAliasObj := types.NewTypeName(token.NoPos, user, "LP", nil)
	ptrAlias := types.NewAlias(ptrAliasObj, types.NewPointer(lexer))

	mk := func(param types.Type) *types.Named {
		obj := types.NewTypeName(token.NoPos, user, "T", nil)
		named := types.NewNamed(obj, types.NewStruct(nil, nil), nil)
		recv := types.NewVar(token.NoPos, user, "v", types.NewPointer(named))
		sig := types.NewSignatureType(recv, nil, nil, types.NewTuple(types.NewVar(token.NoPos, user, "l", param)), nil, false)
		named.AddMethod(types.NewFunc(token.NoPos, user, "ReadZJSON", sig))
		return named
	}
	for name, param := range map[string]types.Type{
		"*zerojson.Lexer": types.NewPointer(lexer),
		"*LexerAlias":     types.NewPointer(alias),
		"LP (= *Lexer)":   ptrAlias,
	} {
		if !hasReadZJSONSig(mk(param)) {
			t.Errorf("ReadZJSON(%s) not recognized", name)
		}
	}
	other := types.NewNamed(types.NewTypeName(token.NoPos, user, "Lexer", nil), types.NewStruct(nil, nil), nil)
	if hasReadZJSONSig(mk(types.NewPointer(other))) {
		t.Error("a user type named Lexer must not be recognized")
	}
}
