package files

import (
	"fmt"
	"net/http"
	"time"

	"golang.org/x/sys/unix"

	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
)

// stat returns the Entry for one path (spec 040 T005). A symlink at the final
// component is reported as itself (lstat), like /files/list; it is never followed.
func (h *handler) stat(w http.ResponseWriter, req *http.Request) {
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	e, err := h.statEntry(comps)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, e)
}

func (h *handler) statEntry(comps []string) (Entry, error) {
	rootFD, err := openRoot(h.root)
	if err != nil {
		return Entry{}, err
	}
	defer closeFD(rootFD)
	dirFD, name, err := h.locate(rootFD, comps, false)
	if err != nil {
		return Entry{}, err
	}
	defer closeFD(dirFD)
	var st unix.Stat_t
	if name == "" {
		err = unix.Fstat(dirFD, &st)
	} else {
		h.fire("stat", parentsOf(comps), name)
		err = unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("stat %q: %w", name, err)
	}
	mode := fileModeFromStat(st.Mode)
	path := "/"
	if name != "" {
		path = entryPath(comps[:len(comps)-1], name)
	}
	return Entry{
		Name:    name,
		Path:    path,
		Size:    st.Size,
		Mode:    mode.String(),
		Dir:     mode.IsDir(),
		ModTime: time.Unix(st.Mtim.Unix()).UTC().Format(time.RFC3339),
	}, nil
}
