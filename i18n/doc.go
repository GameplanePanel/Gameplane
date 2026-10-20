// Package i18n holds Gameplane's shared language packs: the English reference
// catalog, the translated packs under locales/, and the embedded file system
// that other modules import at build time.
//
// The ICU message formatter lives in package icu, pack validation in package
// pack, runtime lookup in package catalog, and the CI checker in
// cmd/i18n-check.
package i18n
