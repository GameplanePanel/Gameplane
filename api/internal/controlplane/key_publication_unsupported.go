//go:build !unix

package controlplane

func lockKeyPublication(_ string) (func(), error) { return nil, validateKeyPlatform() }
func recoverKeyAliases(_ string) error            { return validateKeyPlatform() }
