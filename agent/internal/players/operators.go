package players

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/GameplanePanel/gameplane/agent/internal/files"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
)

const maxOperatorsBytes = 1 << 20

// Operator is the public identity of a configured Minecraft Java operator.
// Permission level and player-limit overrides stay in the source file.
type Operator struct {
	Name string `json:"name"`
	UUID string `json:"uuid,omitempty"`
}

func (h *handler) capabilities() Capabilities {
	capabilities := h.cmdr.Capabilities()
	capabilities.Operators = h.operatorsRoot != ""
	return capabilities
}

func (h *handler) operators(w http.ResponseWriter, _ *http.Request) {
	if h.operatorsRoot == "" {
		http.Error(w, "operator list unsupported", http.StatusNotImplemented)
		return
	}
	operators, err := readOperators(h.operatorsRoot)
	if err != nil {
		slog.Warn("read operator list", "err", err)
		http.Error(w, "operator list unavailable", http.StatusBadGateway)
		return
	}
	httpjson.Write(w, http.StatusOK, operators)
}

func readOperators(root string) ([]Operator, error) {
	// The game writes this fixed path. Never accept a file path from callers.
	f, err := files.OpenRegularFile(root, "ops.json")
	if errors.Is(err, os.ErrNotExist) {
		return []Operator{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxOperatorsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxOperatorsBytes {
		return nil, errors.New("operator list exceeds size limit")
	}
	var operators []Operator
	if err := json.Unmarshal(data, &operators); err != nil {
		return nil, err
	}
	if operators == nil {
		return nil, errors.New("operator list must be an array")
	}
	for _, operator := range operators {
		if strings.TrimSpace(operator.Name) == "" && strings.TrimSpace(operator.UUID) == "" {
			return nil, errors.New("operator has no identity")
		}
	}
	slices.SortFunc(operators, func(a, b Operator) int {
		if byName := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); byName != 0 {
			return byName
		}
		return strings.Compare(a.UUID, b.UUID)
	})
	return operators, nil
}
