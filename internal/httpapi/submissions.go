package httpapi

import (
	"mailhearth/internal/secrets"
	"net/http"
)

func (s *Server) routesSubmissions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/submissions", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		id, err := pathInt(r, "id")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.Submissions(r.Context(), p.OrgID, p.Member.ID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}))
	mux.HandleFunc("GET /api/mail/mailboxes/{id}/submissions/{submissionId}", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		id, err := pathInt(r, "id")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.Submission(r.Context(), p.OrgID, p.Member.ID, id, r.PathValue("submissionId"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}))
	mux.HandleFunc("POST /api/mail/mailboxes/{id}/submissions/{submissionId}/retry-sent-copy", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		id, err := pathInt(r, "id")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in struct{}
		if err := readJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.Svc.RetrySubmissionSentCopy(r.Context(), p.OrgID, p.Member.ID, id, r.PathValue("submissionId"), secrets.HashToken(p.Token))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, v)
	}))
}
