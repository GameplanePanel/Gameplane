module github.com/GameplanePanel/gameplane/gp-module

go 1.26.0

// i18n is an in-repo module (no published version); resolve it locally
// both inside the go.work workspace and in standalone/Docker builds.
replace github.com/GameplanePanel/gameplane/i18n => ../i18n

require (
	github.com/GameplanePanel/gameplane/i18n v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/text v0.42.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	k8s.io/apimachinery v0.37.1
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
)
