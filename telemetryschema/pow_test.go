package telemetryschema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fixed vectors for the challenge "gameplane-pow-vector". They were computed
// once with this package and pin the hash input (challenge, ":", decimal
// nonce), the bit counting and the solver's search order (smallest nonce
// first). sha256 hex is of "gameplane-pow-vector:<nonce>".
const powVectorChallenge = "gameplane-pow-vector"

var powVectors = []struct {
	bits  int
	nonce uint64
	hash  string
}{
	{0, 0, "dafdb5e8bf7fc00dd10e1c84e59c2b4f864f4864a9a72e9dded6cbce44fd3743"},
	{8, 450, "00d28d8005d89a9dea62f4f3b973d1fc8bc97d61356061d3abaa54a13b255803"},
	{12, 21441, "000b290e819f5fe129f9049820f6a016b6ac9f65170c9610b2854a94a9166c9a"},
	{16, 27322, "0000083735a5d0e3c57edbd152e4d720e7225352d2c0275cc1cab3dab9b61433"},
}

func TestPoWConstants(t *testing.T) {
	if PoWHeader != "Gameplane-Telemetry-PoW" {
		t.Errorf("PoWHeader = %q", PoWHeader)
	}
	if MaxPoWBits != 26 {
		t.Errorf("MaxPoWBits = %d, want 26 (OD-5)", MaxPoWBits)
	}
}

func TestPoWOKMatchesTheFixedVectors(t *testing.T) {
	for _, v := range powVectors {
		sum := sha256.Sum256([]byte(powVectorChallenge + ":" + strconv.FormatUint(v.nonce, 10)))
		if got := hex.EncodeToString(sum[:]); got != v.hash {
			t.Fatalf("vector %d bits: sha256 = %s, want %s", v.bits, got, v.hash)
		}
		if !PoWOK(powVectorChallenge, v.nonce, v.bits) {
			t.Errorf("PoWOK(%d bits, nonce %d) = false, want true", v.bits, v.nonce)
		}
	}
	// 000b... has exactly 12 leading zero bits, 00d2... exactly 8.
	if PoWOK(powVectorChallenge, 21441, 13) || PoWOK(powVectorChallenge, 450, 9) {
		t.Error("PoWOK accepted more leading zero bits than the hash has")
	}
	if PoWOK(powVectorChallenge, 21440, 12) {
		t.Error("PoWOK accepted a nonce that is not a solution")
	}
}

func TestPoWOKEdges(t *testing.T) {
	if !PoWOK("x", 1, 0) || !PoWOK("x", 1, -5) {
		t.Error("0 or fewer bits is always satisfied")
	}
	if PoWOK("x", 1, 257) {
		t.Error("more than 256 bits can never be satisfied")
	}
	// The all-zero hash case cannot be built, but the byte loop's whole-byte
	// and partial-byte branches are both covered by the vectors above.
}

func TestSolvePoWFindsTheSmallestNonce(t *testing.T) {
	for _, v := range powVectors {
		nonce, err := SolvePoW(context.Background(), powVectorChallenge, v.bits)
		if err != nil || nonce != v.nonce {
			t.Fatalf("SolvePoW(%d bits) = %d, %v, want %d", v.bits, nonce, err, v.nonce)
		}
	}
}

func TestSolvePoWRefusesOutOfRangeDifficulty(t *testing.T) {
	for _, bits := range []int{-1, MaxPoWBits + 1, 1000} {
		if _, err := SolvePoW(context.Background(), "c", bits); !errors.Is(err, ErrPoWDifficulty) {
			t.Errorf("SolvePoW(%d bits) = %v, want ErrPoWDifficulty", bits, err)
		}
	}
}

func TestSolvePoWStopsWhenTheContextIsDone(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SolvePoW(cancelled, "c", MaxPoWBits); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: err = %v, want context.Canceled", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := SolvePoW(expired, "c", MaxPoWBits); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expired context: err = %v, want context.DeadlineExceeded", err)
	}
}

func TestSolvePoWChecksTheContextWhileSearching(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		// A 26-bit solution takes tens of millions of hashes on average, far
		// longer than this test waits.
		_, err := SolvePoW(ctx, "a-challenge-nobody-solves-quickly", MaxPoWBits)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		// A very lucky solution is not an error; anything else must be the
		// cancellation.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SolvePoW did not stop after its context was cancelled")
	}
}

func TestFormatAndParsePoWRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		challenge string
		nonce     uint64
		header    string
	}{
		{"abc.def", 0, "abc.def:0"},
		{"abc.def", 42, "abc.def:42"},
		{"has:colons:inside", 7, "has:colons:inside:7"},
		{"c", 18446744073709551615, "c:18446744073709551615"},
		{strings.Repeat("a", MaxChallengeLen), 1, strings.Repeat("a", MaxChallengeLen) + ":1"},
	} {
		if got := FormatPoW(tc.challenge, tc.nonce); got != tc.header {
			t.Errorf("FormatPoW(%q, %d) = %q, want %q", tc.challenge, tc.nonce, got, tc.header)
		}
		challenge, nonce, err := ParsePoW(tc.header)
		if err != nil || challenge != tc.challenge || nonce != tc.nonce {
			t.Errorf("ParsePoW(%q) = %q, %d, %v, want %q, %d", tc.header, challenge, nonce, err, tc.challenge, tc.nonce)
		}
	}
}

func TestParsePoWRejectsMalformedHeaders(t *testing.T) {
	for name, header := range map[string]string{
		"empty":                    "",
		"no separator":             "abc",
		"empty challenge":          ":5",
		"empty nonce":              "abc:",
		"negative nonce":           "abc:-1",
		"signed nonce":             "abc:+1",
		"leading zero":             "abc:01",
		"not digits":               "abc:1x",
		"hex":                      "abc:0x10",
		"overflows uint64":         "abc:18446744073709551616",
		"challenge over the limit": strings.Repeat("a", MaxChallengeLen+1) + ":1",
	} {
		if _, _, err := ParsePoW(header); !errors.Is(err, ErrBadPoW) {
			t.Errorf("%s: ParsePoW(%q) err = %v, want ErrBadPoW", name, header, err)
		}
	}
}

func TestSolvedHeaderParsesAndVerifies(t *testing.T) {
	nonce, err := SolvePoW(context.Background(), "round.trip", 10)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	challenge, got, err := ParsePoW(FormatPoW("round.trip", nonce))
	if err != nil || !PoWOK(challenge, got, 10) {
		t.Fatalf("parsed %q, %d, %v: not a solution", challenge, got, err)
	}
}
