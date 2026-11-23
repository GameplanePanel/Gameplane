package gateway

import (
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
)

func TestGatewayFilesUploadLimit(t *testing.T) {
	h, _ := newFixture(t)

	cfg := h.cfg
	cfg.FilesMaxUploadBytes = -1
	if _, err := NewHandler(cfg, h.client); err == nil {
		t.Fatal("negative upload limit accepted")
	}

	for _, limit := range []int64{0, gatewayprotocol.DefaultStreamBodyLimit, 1 << 20} {
		cfg := h.cfg
		cfg.FilesMaxUploadBytes = limit
		if _, err := NewHandler(cfg, h.client); err != nil {
			t.Fatalf("limit %d refused: %v", limit, err)
		}
	}
}
