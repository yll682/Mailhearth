package httpapi

import (
	"mailhearth/internal/core"
	"net/http"
)

func (s *Server) routesFolderMapping(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/folder-mapping", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		id, err := pathInt(r, "id")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.FolderMapping(r.Context(), p.OrgID, p.Member.ID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}))
	mux.HandleFunc("PUT /api/mail/mailboxes/{id}/folder-mapping", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		id, err := pathInt(r, "id")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in core.UpdateFolderMappingInput
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		ctx, cancel := s.Svc.RegisterMailRequest(r.Context(), p.Member.ID, id, p.Token)
		defer cancel()
		v, err := s.Svc.UpdateFolderMapping(ctx, p.OrgID, p.Member.ID, id, in)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}))
}
