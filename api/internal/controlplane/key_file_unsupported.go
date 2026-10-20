//go:build !unix

package controlplane

import (
	"errors"
	"os"
)

func validateKeyPlatform() error {
	return errors.New("native standalone key permission validation is unavailable on this platform; run the API in a Linux container")
}

func openKeyHandle(_ *os.Root, _ string) (*os.File, error) {
	return nil, validateKeyPlatform()
}

func validateKeyFile(_ os.FileInfo) error {
	return errors.New("management key ownership validation is unavailable on this platform")
}
