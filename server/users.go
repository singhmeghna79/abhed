package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// Local-account administration for the single-tenant server. The CLI twin is
// `abhed user`; this is the same set of facts reachable from the console.

type adminUser struct {
	Username string    `json:"username"`
	Email    string    `json:"email,omitempty"`
	Tenant   string    `json:"tenant"`
	Groups   []string  `json:"groups,omitempty"`
	Admin    bool      `json:"admin"`
	Created  time.Time `json:"created_at"`
}

// listUsers reports the accounts on this deployment.
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	local := s.LocalAuth()
	if local == nil {
		WriteError(w, http.StatusNotImplemented,
			"this deployment does not hold its own accounts")
		return
	}
	users, err := local.ListUsers(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "could not list accounts")
		return
	}
	admin := s.adminGroup()
	out := make([]adminUser, 0, len(users))
	for _, u := range users {
		au := adminUser{
			Username: u.Username, Email: u.Email, Tenant: u.Tenant,
			Groups: u.Groups, Created: u.CreatedAt,
		}
		for _, g := range u.Groups {
			if g == admin {
				au.Admin = true
			}
		}
		out = append(out, au)
	}
	WriteJSON(w, http.StatusOK, out)
}

// setUserAdmin grants or revokes administrator rights.
func (s *Server) setUserAdmin(w http.ResponseWriter, r *http.Request) {
	local := s.LocalAuth()
	if local == nil {
		WriteError(w, http.StatusNotImplemented,
			"this deployment does not hold its own accounts")
		return
	}
	var req struct {
		Username string `json:"username"`
		Admin    bool   `json:"admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Username == "" {
		WriteError(w, http.StatusBadRequest, "username is required")
		return
	}
	// Removing your own admin rights would leave a deployment with no way back
	// in if you are the only administrator. Refuse rather than let someone
	// lock themselves out of their own settings.
	if !req.Admin && req.Username == UserOf(r.Context()) {
		WriteError(w, http.StatusConflict,
			"you cannot remove your own administrator rights")
		return
	}
	if err := local.SetGroups(r.Context(), req.Username,
		s.adminGroup(), req.Admin); err != nil {
		WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	s.log.Info("admin rights changed", "user", req.Username,
		"admin", req.Admin, "by", UserOf(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}
