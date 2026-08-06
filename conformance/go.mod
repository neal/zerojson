module github.com/neal/zerojson/conformance

go 1.26

require (
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68
	github.com/google/uuid v1.6.0
	github.com/mailru/easyjson v0.9.2
	github.com/neal/zerojson v0.0.0
)

require github.com/josharian/intern v1.0.0 // indirect

require (
	github.com/neal/zerojson/cmd/zerojsongen v0.0.0-00010101000000-000000000000 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
)

replace (
	github.com/neal/zerojson => ../
	github.com/neal/zerojson/cmd/zerojsongen => ../cmd/zerojsongen
)

tool github.com/neal/zerojson/cmd/zerojsongen
