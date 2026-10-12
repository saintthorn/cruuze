package server

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"cruuze/server/internal/api"
	"cruuze/server/internal/config"
	"cruuze/server/internal/db/dbgen"
)

const (
	signupOpen    = string(api.SignupModeOpen)
	signupDomains = string(api.SignupModeDomains)

	creationAdmins = string(api.InstanceSettingsWorkspaceCreationAdmins)
)

var (
	errSignupClosed = &apiError{http.StatusForbidden, "signup_closed", "This server only takes people who were invited. Ask someone in your workspace for an invite."}
	domainPattern   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// isInstanceAdmin reports whether the user runs this install: the first
// person who signed in, or anyone named in INSTANCE_ADMINS.
func (s *Server) isInstanceAdmin(user dbgen.User) bool {
	return user.InstanceAdmin || slices.Contains(s.cfg.InstanceAdmins, strings.ToLower(user.Email))
}

func domainAllowed(settings dbgen.InstanceSetting, email string) bool {
	domain := email[strings.LastIndex(email, "@")+1:]
	return settings.Signup == signupDomains && slices.Contains(settings.SignupDomains, strings.ToLower(domain))
}

// allowSignup decides whether an address may sign in. Anyone with an account
// may; a new address needs signups to be open, an invite, an allowed domain,
// or to be the one setting the server up.
func (s *Server) allowSignup(ctx context.Context, q *dbgen.Queries, email string) (bool, error) {
	settings, err := q.GetInstanceSettings(ctx)
	if err != nil {
		return false, err
	}
	if settings.Signup == signupOpen || slices.Contains(s.cfg.InstanceAdmins, email) || domainAllowed(settings, email) {
		return true, nil
	}
	standing, err := q.SignupStanding(ctx, email)
	if err != nil {
		return false, err
	}
	return standing.Known || standing.Invited || standing.First, nil
}

// mayCreateWorkspace decides whether a user may start a workspace. Where
// signups are restricted, someone who only came in through a customer portal
// may not.
func (s *Server) mayCreateWorkspace(ctx context.Context, q *dbgen.Queries, user dbgen.User) (bool, error) {
	if s.isInstanceAdmin(user) {
		return true, nil
	}
	settings, err := q.GetInstanceSettings(ctx)
	if err != nil || settings.WorkspaceCreation == creationAdmins {
		return false, err
	}
	if settings.Signup == signupOpen || domainAllowed(settings, user.Email) {
		return true, nil
	}
	return q.IsMemberAnywhere(ctx, user.ID)
}

func (s *Server) requireInstanceAdmin(w http.ResponseWriter, r *http.Request) bool {
	// An API key is tied to one workspace; the install is not its business.
	if !s.requireBrowser(w, r) {
		return false
	}
	if !s.isInstanceAdmin(currentSession(r).User) {
		forbidden(w)
		return false
	}
	return true
}

func (s *Server) GetInstance(w http.ResponseWriter, r *http.Request) {
	if !s.requireInstanceAdmin(w, r) {
		return
	}
	settings, err := s.q.GetInstanceSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeInstance(w, r, settings)
}

func (s *Server) writeInstance(w http.ResponseWriter, r *http.Request, settings dbgen.InstanceSetting) {
	stats, err := s.q.InstanceStats(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	admins, err := s.q.ListInstanceAdminEmails(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	admins = append(admins, s.cfg.InstanceAdmins...)
	slices.Sort(admins)
	writeJSON(w, http.StatusOK, api.InstanceSettings{
		Version:           config.Version,
		Signup:            api.SignupMode(settings.Signup),
		SignupDomains:     settings.SignupDomains,
		WorkspaceCreation: api.InstanceSettingsWorkspaceCreation(settings.WorkspaceCreation),
		Users:             stats.Users,
		Workspaces:        stats.Workspaces,
		Issues:            stats.Issues,
		Admins:            slices.Compact(admins),
	})
}

func (s *Server) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	if !s.requireInstanceAdmin(w, r) {
		return
	}
	var req api.UpdateInstanceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var params dbgen.UpdateInstanceSettingsParams
	if req.Signup != nil {
		if !slices.Contains([]api.SignupMode{api.SignupModeOpen, api.SignupModeInvited, api.SignupModeDomains}, *req.Signup) {
			writeError(w, http.StatusBadRequest, "invalid_signup", "Signups are open, invited or domains.")
			return
		}
		signup := string(*req.Signup)
		params.Signup = &signup
	}
	if req.WorkspaceCreation != nil {
		creation := string(*req.WorkspaceCreation)
		if creation != creationAdmins && creation != string(api.InstanceSettingsWorkspaceCreationAnyone) {
			writeError(w, http.StatusBadRequest, "invalid_workspace_creation", "Workspaces are created by anyone or by admins.")
			return
		}
		params.WorkspaceCreation = &creation
	}
	if req.SignupDomains != nil {
		domains := []string{}
		for _, raw := range *req.SignupDomains {
			domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
			if domain == "" {
				continue
			}
			if len(domain) > 253 || !domainPattern.MatchString(domain) {
				writeError(w, http.StatusBadRequest, "invalid_domain", "\""+truncate(raw, 60)+"\" isn't a domain like example.com.")
				return
			}
			domains = append(domains, domain)
		}
		if len(domains) > 50 {
			writeError(w, http.StatusBadRequest, "invalid_domain", "At most 50 domains.")
			return
		}
		slices.Sort(domains)
		params.SignupDomains = slices.Compact(domains)
	}
	settings, err := s.q.UpdateInstanceSettings(r.Context(), params)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeInstance(w, r, settings)
}
