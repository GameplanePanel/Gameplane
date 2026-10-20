package telemetryschema

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PoWHeader is the HTTP request header that carries a solved proof-of-work
// challenge: "<challenge>:<nonce>" (research R21). A receiver that requires
// proof-of-work refuses a report without it.
const PoWHeader = "Gameplane-Telemetry-PoW"

// MaxPoWBits is the hardest challenge an install will solve (OD-5). SolvePoW
// refuses anything above it, so a misconfigured provider cannot burn an
// install's CPU. Its cost is about 2^26 hashes.
const MaxPoWBits = 26

// MaxChallengeLen bounds the challenge part of a PoWHeader value. A
// receiver's challenge is about 60 characters; the bound stops a hostile
// header from being hashed at any length.
const MaxChallengeLen = 256

// solveCheckEvery is how many nonces SolvePoW tries between context checks.
const solveCheckEvery = 1 << 12

var (
	// ErrBadPoW is wrapped by every ParsePoW failure.
	ErrBadPoW = errors.New("bad proof-of-work header")
	// ErrPoWDifficulty is wrapped by the SolvePoW error for a difficulty
	// below 0 or above MaxPoWBits.
	ErrPoWDifficulty = errors.New("proof-of-work difficulty out of range")
)

// PoWOK reports whether SHA-256(challenge + ":" + decimal nonce) starts with
// at least bits zero bits. A bits value of 0 or less is always satisfied.
func PoWOK(challenge string, nonce uint64, bits int) bool {
	if bits <= 0 {
		return true
	}
	buf := append([]byte(challenge), ':')
	buf = strconv.AppendUint(buf, nonce, 10)
	return leadingZeroBits(sha256.Sum256(buf)) >= bits
}

// SolvePoW finds the smallest nonce for which PoWOK(challenge, nonce, bits)
// holds. It runs on the calling goroutine only and checks ctx every few
// thousand attempts, returning an error that wraps ctx's error when it is
// done. It errors, without hashing, when bits is below 0 or above
// MaxPoWBits.
func SolvePoW(ctx context.Context, challenge string, bits int) (uint64, error) {
	if bits < 0 || bits > MaxPoWBits {
		return 0, fmt.Errorf("telemetryschema: solve proof-of-work: %w: %d, want 0 to %d", ErrPoWDifficulty, bits, MaxPoWBits)
	}
	prefix := len(challenge) + 1
	buf := make([]byte, prefix, prefix+20)
	copy(buf, challenge)
	buf[prefix-1] = ':'
	for nonce := uint64(0); ; nonce++ {
		if nonce%solveCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return 0, fmt.Errorf("telemetryschema: solve proof-of-work: %w", err)
			}
		}
		buf = strconv.AppendUint(buf[:prefix], nonce, 10)
		if leadingZeroBits(sha256.Sum256(buf)) >= bits {
			return nonce, nil
		}
	}
}

// FormatPoW returns the PoWHeader value for a solved challenge.
func FormatPoW(challenge string, nonce uint64) string {
	return challenge + ":" + strconv.FormatUint(nonce, 10)
}

// ParsePoW splits a PoWHeader value at its last ':' into the challenge and
// the nonce. The nonce must be plain decimal (digits only, no sign, no
// leading zeros, within uint64) and the challenge non-empty and at most
// MaxChallengeLen bytes. Every failure wraps ErrBadPoW.
func ParsePoW(header string) (string, uint64, error) {
	i := strings.LastIndexByte(header, ':')
	if i < 0 {
		return "", 0, fmt.Errorf("%w: no ':' before the nonce", ErrBadPoW)
	}
	challenge, raw := header[:i], header[i+1:]
	if challenge == "" || len(challenge) > MaxChallengeLen {
		return "", 0, fmt.Errorf("%w: challenge must be 1 to %d bytes", ErrBadPoW, MaxChallengeLen)
	}
	nonce, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || strconv.FormatUint(nonce, 10) != raw {
		return "", 0, fmt.Errorf("%w: nonce must be a plain decimal uint64", ErrBadPoW)
	}
	return challenge, nonce, nil
}

// leadingZeroBits counts the zero bits at the start of sum.
func leadingZeroBits(sum [sha256.Size]byte) int {
	n := 0
	for _, b := range sum {
		if b == 0 {
			n += 8
			continue
		}
		for mask := byte(0x80); b&mask == 0; mask >>= 1 {
			n++
		}
		return n
	}
	return n
}
