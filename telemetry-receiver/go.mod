module github.com/GameplanePanel/gameplane/telemetry-receiver

go 1.26.0

require (
	github.com/GameplanePanel/gameplane/telemetryschema v0.0.0-00010101000000-000000000000
	github.com/prometheus/client_golang v1.25.0
	golang.org/x/mod v0.42.0
	modernc.org/sqlite v1.60.1
)

// telemetryschema is an in-repo module (no published version); resolve it
// locally both inside the go.work workspace and for standalone
// `go build ./telemetry-receiver/...`.
replace github.com/GameplanePanel/gameplane/telemetryschema => ../telemetryschema

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.72.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.48.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
