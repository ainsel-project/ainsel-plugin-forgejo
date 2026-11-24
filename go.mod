module github.com/ainsel-project/ainsel-plugin-forgejo

// go directive: the language level this module is written at. Kept explicit
// rather than derived so a toolchain on a developer machine cannot silently
// raise it.
go 1.26.0

// Required by the govulncheck gate in ci.yml: with only `go 1.26.0`, GitHub's
// setup-go resolves the go1.26.0 toolchain, whose stdlib carries reachable
// advisories (GO-2026-6218, GO-2026-6090, GO-2026-6089, ... fixed in go1.26.6),
// so the gate failed on the toolchain rather than on this module's deps.
// setup-go reads this line; the golang:1.26 Docker base is already >= 1.26.6.
toolchain go1.26.11

require (
	github.com/mark3labs/mcp-go v1.2.1
	modernc.org/sqlite v1.61.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
