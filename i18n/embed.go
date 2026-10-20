package i18n

import "embed"

// Locales holds every language pack under locales/<tag>/, including the
// English reference (locales/en/app.json). go:embed paths cannot contain "..",
// so the embed directive must live in this root package while the consumers
// (package catalog) read the files from here.
//
//go:embed locales/*/*.json
var Locales embed.FS
