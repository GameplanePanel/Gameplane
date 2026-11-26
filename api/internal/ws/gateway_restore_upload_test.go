package ws

import (
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
)

// closeIfAny closes the response body of a call that returned one, so a
// rejected call never leaks its connection.
func closeIfAny(resp *http.Response) {
	if resp != nil {
		_ = resp.Body.Close()
	}
}

// RestoreUploadStream refuses malformed calls before any network use: a
// cluster that is not the target's, no body, an unknown length, and a server
// UID that the registered GameServer no longer has.
func TestRestoreUploadStreamRejectsBadCallsWithoutDialing(t *testing.T) {
	resolver, _, _ := gatewayFixture(t, "https://gateway.test:8443")
	client := &CaptureGatewayClient{resolver: resolver}
	target := gatewayprotocol.RestoreUploadTarget{
		Target:     gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "remote-uid"},
		Restore:    "rs-one",
		RestoreUID: "restore-uid",
	}
	body := strings.NewReader("archive")
	for name, call := range map[string]func() error{
		"cluster mismatch": func() error {
			resp, err := client.RestoreUploadStream(t.Context(), "local", target, body, 7)
			closeIfAny(resp)
			return err
		},
		"nil body": func() error {
			resp, err := client.RestoreUploadStream(t.Context(), "remote", target, nil, 7)
			closeIfAny(resp)
			return err
		},
		"unknown length": func() error {
			resp, err := client.RestoreUploadStream(t.Context(), "remote", target, body, 0)
			closeIfAny(resp)
			return err
		},
		"stale server uid": func() error {
			stale := target
			stale.UID = "replaced-uid"
			resp, err := client.RestoreUploadStream(t.Context(), "remote", stale, body, 7)
			closeIfAny(resp)
			return err
		},
		"invalid restore name": func() error {
			bad := target
			bad.Restore = "Bad/Name"
			resp, err := client.RestoreUploadStream(t.Context(), "remote", bad, body, 7)
			closeIfAny(resp)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("accepted a bad restore upload call")
			}
		})
	}
}
