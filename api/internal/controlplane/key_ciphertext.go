package controlplane

import (
	"encoding/base64"
	"errors"
	"strings"
)

const ciphertextVersion = "gpk1"

func (s *objects) ciphertextHeader() string { return ciphertextVersion + "." + s.keyID + "." }

func (s *objects) ciphertextAAD(name, uid string, version int64) []byte {
	// Authenticating the header also prevents stripping it to force legacy
	// decoding. The full key ID is a domain-separated digest of a random key.
	return append([]byte(s.ciphertextHeader()+"\x00"), s.aad(name, uid, version)...)
}

func (s *objects) openCiphertext(name, uid string, version int64, payload string) ([]byte, error) {
	aad := s.aad(name, uid, version)
	encoded := payload
	if strings.Contains(payload, ".") {
		header := s.ciphertextHeader()
		if !strings.HasPrefix(payload, header) {
			return nil, errors.New("unsupported management credential format or encryption key identity")
		}
		encoded = strings.TrimPrefix(payload, header)
		aad = s.ciphertextAAD(name, uid, version)
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return nil, errors.New("invalid encrypted management credential")
	}
	data, err := s.aead.Open(nil, sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():], aad)
	if err != nil {
		return nil, errors.New("management credential authentication failed; verify the database and encryption key")
	}
	return data, nil
}
