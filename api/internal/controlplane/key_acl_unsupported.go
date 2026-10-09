//go:build unix && !linux

package controlplane

import (
	"errors"
	"os"
)

func validateKeyACLPlatform() error {
	return errors.New("management key ACL validation is unavailable on this platform; run the API in a Linux container")
}

func validateKeyACL(_ *os.File) error { return validateKeyACLPlatform() }
