// Package forgejo is the plugin's client for the Forgejo instance an install is
// set up on. It is a setup-time client, not a work-time one: everything here is
// called by the wizard while an install is created or removed (the bot accounts,
// their teams, their tokens, the org webhook), and never to read issues or pull
// requests — work data comes over the forge MCP server instead.
//
// Two kinds of caller, and they are not interchangeable:
//
//   - Client methods that need no credential, of which there is exactly one —
//     MintToken, which authenticates as the bot with its one-time password
//     because Forgejo mints a user's token only for that user (fact V8: an admin
//     minting with Sudo answers 401).
//   - Admin, what Client.Admin hands back for a site administrator's token: the
//     account, team and webhook work.
//
// Secrets (R4): the admin token, a bot password and a minted token are held only
// to send. They are not in a log line, not in an error, and not in what an Admin
// or a Hook prints — a failure message carries Forgejo's own words, with anything
// this client handed to the instance redacted out of them.
package forgejo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// forgejoTimeout is the ceiling on one Forgejo call. The wizard's Apply runs
// inside a console request and the removal runs inside one too, so a Forgejo that
// has stopped answering has to be given up on here — nothing else will.
const forgejoTimeout = 10 * time.Second

// teamPageSize is the page size the team list is read at, and teamListPages the
// most pages a search will follow. Both are guards on a loop the answer controls:
// the page size is Forgejo's maximum, and the page cap stops a broken instance
// that answers every page with content from spinning the loop forever.
const (
	teamPageSize  = 50
	teamListPages = 100
)

// botEmailDomain is the domain a created bot's address is under. Forgejo requires
// an address on a user it creates over the API; a bot has no mail, and .invalid is
// reserved by RFC 2606 so nothing can ever be delivered there.
const botEmailDomain = "ainsel.invalid"

// hookType is the webhook type Forgejo calls its own payload shape, and
// hookContentType the one the connector's receiver parses.
const (
	hookType        = "forgejo"
	hookContentType = "json"
)

// Client reaches one Forgejo instance. It is the address plus the http client, and
// it holds no credential: the credential belongs to the Admin a call is made
// through, or to the single basic-auth call that carries a bot's password.
type Client struct {
	baseURL string
	hc      *http.Client
}

// New builds the client for one instance's address. A nil http client gets the
// default one, which is the one with the timeout on it. A caller that supplies
// its own gets a shallow copy with the timeout clamped to forgejoTimeout if it
// has none or a longer one, so the 10 s ceiling cannot be dropped by injection.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: forgejoTimeout}
	} else if hc.Timeout == 0 || hc.Timeout > forgejoTimeout {
		// Shallow copy and clamp the timeout.
		copy := *hc
		copy.Timeout = forgejoTimeout
		hc = &copy
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), hc: hc}
}

// Admin returns the client acting with a site administrator's token. The token is
// used for the calls and dropped with the Admin; the wizard never stores it (R4).
func (c *Client) Admin(token string) *Admin {
	return &Admin{c: c, token: token}
}

// Admin is the client plus the admin token every account, team and webhook call is
// made with.
type Admin struct {
	c     *Client
	token string
}

// String says who this is without saying what it holds: an Admin that ends up in a
// log line, or in the %v of the step that holds it, names the instance and the
// fact that a token is in hand, and never the token.
func (a *Admin) String() string {
	return fmt.Sprintf("forgejo.Admin{baseURL: %q, hasToken: %t}", a.c.baseURL, a.token != "")
}

// WhoAmI reads back the account the token belongs to: the login, and whether
// Forgejo calls it an administrator. The wizard's preview blocks on exactly these
// two — a token that is not a site admin cannot do any of the rest.
func (a *Admin) WhoAmI(ctx context.Context) (login string, isAdmin bool, err error) {
	var res struct {
		Login   string `json:"login"`
		IsAdmin bool   `json:"is_admin"`
	}
	if err := a.call(ctx, http.MethodGet, apiPath("user"), nil, nil, &res); err != nil {
		return "", false, err
	}
	return res.Login, res.IsAdmin, nil
}

// -- existence -------------------------------------------------------------------

// OrgExists reports whether the org is there. A 404 is the answer that says it is
// not, and is not an error: the preview asks this to tell a free name from a taken
// one. A Forgejo it cannot reach is an error, because "unreachable" must never be
// read as "free".
func (a *Admin) OrgExists(ctx context.Context, org string) (bool, error) {
	return a.exists(a.call(ctx, http.MethodGet, apiPath("orgs", org), nil, nil, nil))
}

// UserExists reports whether the login is taken — by anyone, including a user
// whose visibility hides them, which is what the bots this plugin creates are.
// That is why this reads the user route rather than a search: with an admin token
// the instance answers for a hidden user too, so a login that exists cannot look
// free. Taking over an existing account is out of the rules (R5).
func (a *Admin) UserExists(ctx context.Context, login string) (bool, error) {
	return a.exists(a.call(ctx, http.MethodGet, apiPath("users", login), nil, nil, nil))
}

// TeamExists reports whether a team of that name is in the org. The list is read
// to its end rather than one page at a time: a team on a later page is still
// taken, and a preview that called such a name free would have Apply fail on a
// duplicate.
func (a *Admin) TeamExists(ctx context.Context, org, name string) (bool, error) {
	_, found, err := a.FindTeam(ctx, org, name)
	return found, err
}

// FindTeam answers the id of the team of that name, which is what DeleteTeam
// needs — TeamExists throws the id away, and a cleanup that only knows the name
// could not reach the delete with it. Not found is not an error: found says it.
// The paging rules are TeamExists's: the list is read to its end, and a name is
// matched exactly.
func (a *Admin) FindTeam(ctx context.Context, org, name string) (int64, bool, error) {
	for page := 1; page <= teamListPages; page++ {
		var res []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		query := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(teamPageSize)}}
		if err := a.call(ctx, http.MethodGet, apiPath("orgs", org, "teams"), query, nil, &res); err != nil {
			// A 404 on the list is the org answering "no teams", which is the same
			// answer as an empty page; anything else that failed is an error.
			if _, err := a.exists(err); err != nil {
				return 0, false, err
			}
			return 0, false, nil
		}
		for _, team := range res {
			// Exact, not a prefix: "ainsel-refiner-demo-other" is another team.
			if team.Name == name {
				return team.ID, true, nil
			}
		}
		// Stop paging when the page is empty: no more teams to read.
		if len(res) == 0 {
			return 0, false, nil
		}
	}
	return 0, false, fmt.Errorf("forgejo: org %q has more teams than %d, its team list was not read to the end", org, teamPageSize*teamListPages)
}

// exists turns the answer to an existence probe into yes/no: a 404 means no and is
// not an error, anything else that failed is an error.
func (a *Admin) exists(err error) (bool, error) {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// -- accounts --------------------------------------------------------------------

// createUserRequest is the body POST /admin/users wants. The field is "username";
// "login" is what the response calls the same thing.
type createUserRequest struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// Explicitly false, not left out: with a missing or true value Forgejo makes
	// the user change their password on the first sign-in, and a bot that mints a
	// token instead would stop there.
	MustChangePassword bool   `json:"must_change_password"`
	Visibility         string `json:"visibility"`
}

// CreateUser creates one bot account: the login, the display name "AInsel <Role>
// (<slug>)", a one-time password the caller throws away once the token is minted,
// private visibility, and no change-password prompt. The password is never
// repeated back — not by this function, and not by an error out of it.
func (a *Admin) CreateUser(ctx context.Context, login, fullName, password string) error {
	body := createUserRequest{
		Username:           login,
		FullName:           fullName,
		Email:              login + "@" + botEmailDomain,
		Password:           password,
		MustChangePassword: false,
		Visibility:         "private",
	}
	return a.call(ctx, http.MethodPost, apiPath("admin", "users"), nil, body, nil, password)
}

// DeleteUser deletes a bot account, and with it its tokens. It purges: a bot that
// left as a husk would still hold its login, and the next install could not use
// the name it is built from. A login that is already gone is deleted, not an
// error — see ignoreNotFound.
func (a *Admin) DeleteUser(ctx context.Context, login string) error {
	return ignoreNotFound(a.call(ctx, http.MethodDelete, apiPath("admin", "users", login), url.Values{"purge": {"true"}}, nil, nil))
}

// MintToken mints a token for a user over basic auth as that user — the only way
// that works (V8): Forgejo mints a token only for the user who asks, so the admin
// token cannot do this call and the bot's one-time password can. The scopes are
// the ones the role needs; the token that comes back goes straight into the role
// image's environment and is never stored.
func (c *Client) MintToken(ctx context.Context, login, password, name string, scopes []string) (string, error) {
	// "sha1" is the field the answer carries the token in; the struct behind it
	// calls the same value Token.
	var res struct {
		Token string `json:"sha1"`
	}
	body := struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}{Name: name, Scopes: scopes}

	err := c.request(ctx, http.MethodPost, apiPath("users", login, "tokens"), nil, body, &res,
		func(req *http.Request) { req.SetBasicAuth(login, password) }, password)
	if err != nil {
		return "", err
	}
	if res.Token == "" {
		// A create that answered without a token is not a minted token, and a
		// caller that stored the empty string would get a 401 on every call.
		return "", fmt.Errorf("forgejo: no token in the answer for %s", login)
	}
	return res.Token, nil
}

// -- teams -----------------------------------------------------------------------

// createTeamRequest is the body POST /orgs/{org}/teams wants. The permissions come
// as units_map, which is the shape that says what the role may do;
// includes_all_repositories is what makes them apply to the org's repos rather
// than to a hand-picked few.
type createTeamRequest struct {
	Name                    string            `json:"name"`
	IncludesAllRepositories bool              `json:"includes_all_repositories"`
	UnitsMap                map[string]string `json:"units_map"`
}

// CreateTeam creates the role's team in the org and answers with its id, which is
// what AddTeamMember and DeleteTeam need.
func (a *Admin) CreateTeam(ctx context.Context, org, name string, units map[string]string) (int64, error) {
	var res struct {
		ID int64 `json:"id"`
	}
	body := createTeamRequest{Name: name, IncludesAllRepositories: true, UnitsMap: units}
	if err := a.call(ctx, http.MethodPost, apiPath("orgs", org, "teams"), nil, body, &res); err != nil {
		return 0, err
	}
	if res.ID == 0 {
		return 0, fmt.Errorf("forgejo: no team id in the answer for %s", name)
	}
	return res.ID, nil
}

// AddTeamMember puts a bot in a team. It is a PUT rather than a POST: adding a
// member twice is fine, which is what a re-apply needs.
func (a *Admin) AddTeamMember(ctx context.Context, teamID int64, login string) error {
	return a.call(ctx, http.MethodPut, apiPath("teams", strconv.FormatInt(teamID, 10), "members", login), nil, nil, nil)
}

// DeleteTeam deletes the role's team. A team that is already gone is deleted, not
// an error — see ignoreNotFound.
func (a *Admin) DeleteTeam(ctx context.Context, id int64) error {
	return ignoreNotFound(a.call(ctx, http.MethodDelete, apiPath("teams", strconv.FormatInt(id, 10)), nil, nil, nil))
}

// -- the org webhook -------------------------------------------------------------

// Hook is the webhook to point at a connector: the endpoint URL, the signing
// secret handed straight from the connector create, and the events. The secret is
// sent and never repeated (R4) — see String.
type Hook struct {
	URL    string
	Secret string
	Events []string
}

// String is the hook without its secret, so a hook in a log line or in the %v of
// the step that holds it says which webhook it is and not what signs it.
func (h Hook) String() string {
	return fmt.Sprintf("forgejo.Hook{URL: %q, secret: set=%t, events: %v}", h.URL, h.Secret != "", h.Events)
}

// hookRequest is the body POST and PATCH /orgs/{org}/hooks want. The URL, the
// secret and the content type travel inside config — that is where Forgejo keeps
// them, and a secret outside it would be read as part of the URL.
type hookRequest struct {
	Type   string            `json:"type"`
	Active bool              `json:"active"`
	Events []string          `json:"events"`
	Config hookRequestConfig `json:"config"`
}

type hookRequestConfig struct {
	URL         string `json:"url"`
	Secret      string `json:"secret"`
	ContentType string `json:"content_type"`
}

func hookBody(h Hook) hookRequest {
	return hookRequest{
		Type:   hookType,
		Active: true,
		Events: h.Events,
		Config: hookRequestConfig{URL: h.URL, Secret: h.Secret, ContentType: hookContentType},
	}
}

// CreateOrgHook points the org's webhook at a connector and answers with the hook
// id, which goes on the install row: it is what the re-apply loop re-points and
// what removal deletes.
func (a *Admin) CreateOrgHook(ctx context.Context, org string, h Hook) (int64, error) {
	var res struct {
		ID int64 `json:"id"`
	}
	if err := a.call(ctx, http.MethodPost, apiPath("orgs", org, "hooks"), nil, hookBody(h), &res, h.Secret); err != nil {
		return 0, err
	}
	if res.ID == 0 {
		return 0, fmt.Errorf("forgejo: no hook id in the answer for %s", org)
	}
	return res.ID, nil
}

// OrgHook is one of the org's webhooks as the list answers it: enough to name it
// to an admin — the id, where it points, and what it delivers — and enough to
// delete it. The URL is the one field a leftover is recognised by; the secret is
// never in the list answer, and this type carries no field for it.
type OrgHook struct {
	ID     int64
	Type   string
	Active bool
	URL    string
	Events []string
}

// ListOrgHooks reads the org's webhooks. The setup otherwise knows only the one
// hook id its own install row carries; the orphan sweep needs the whole list to
// find a hook whose row is gone. Forgejo answers the list in full — it is not
// paginated the way the team list is.
func (a *Admin) ListOrgHooks(ctx context.Context, org string) ([]OrgHook, error) {
	var res []struct {
		ID     int64  `json:"id"`
		Type   string `json:"type"`
		Active bool   `json:"active"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
		Events []string `json:"events"`
	}
	if err := a.call(ctx, http.MethodGet, apiPath("orgs", org, "hooks"), nil, nil, &res); err != nil {
		return nil, err
	}
	out := make([]OrgHook, 0, len(res))
	for _, h := range res {
		out = append(out, OrgHook{ID: h.ID, Type: h.Type, Active: h.Active, URL: h.Config.URL, Events: h.Events})
	}
	return out, nil
}

// UpdateOrgHook sets the whole hook again — URL, secret, events, active. A full
// write rather than a patch of one field is what makes it the repair the re-apply
// loop uses: the hook it leaves behind equals the install, whatever drifted.
func (a *Admin) UpdateOrgHook(ctx context.Context, org string, id int64, h Hook) error {
	return a.call(ctx, http.MethodPatch, apiPath("orgs", org, "hooks", strconv.FormatInt(id, 10)), nil, hookBody(h), nil, h.Secret)
}

// DeleteOrgHook removes the org's webhook, which stops deliveries; the tokens the
// connector holds are revoked on the AInsel side. Nothing secret goes out on it,
// so there is nothing to redact out of a failure message. A hook that is already
// gone is deleted, not an error — see ignoreNotFound.
func (a *Admin) DeleteOrgHook(ctx context.Context, org string, id int64) error {
	return ignoreNotFound(a.call(ctx, http.MethodDelete, apiPath("orgs", org, "hooks", strconv.FormatInt(id, 10)), nil, nil, nil))
}

// ignoreNotFound turns "the resource is already gone" into success on a delete.
// A retried removal re-attempts what an earlier pass already deleted, and a
// delete of a gone resource that errored would keep a row alive forever: the
// removal could never converge. Forgejo's own answer to "delete what is not
// there" is 404, and 404 here means the goal is met.
func ignoreNotFound(err error) error {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// -- the plumbing ----------------------------------------------------------------

// StatusError is Forgejo answering with a status that is not a success. The code
// is what a caller branches on — 401 "the token is not valid", 403 "not a site
// admin", 404 "not there", 422 "that name is taken" — and Message is Forgejo's own
// words, which is what the wizard shows the admin. Neither carries a secret: the
// message is redacted before it is stored here.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("forgejo returned %d", e.Code)
	}
	return fmt.Sprintf("forgejo returned %d: %s", e.Code, e.Message)
}

// call is a request made as the admin: the token goes in the Authorization header
// (Forgejo's scheme is "token", not "bearer"), and the token itself is always one
// of the secrets to keep out of the error.
func (a *Admin) call(ctx context.Context, method, path string, query url.Values, body, out any, secrets ...string) error {
	token := a.token
	return a.c.request(ctx, method, path, query, body, out, func(req *http.Request) {
		req.Header.Set("Authorization", "token "+token)
	}, append([]string{token}, secrets...)...)
}

// request is the one place a Forgejo call is built, sent, and turned into either a
// decoded answer or an error.
//
// authorize puts the credential on the request — which is a header, so the URL
// that a transport error names holds no secret. secrets lists the strings that
// must not come back in an error even if the instance echoes them: the token or
// password just sent, and a webhook secret.
func (c *Client) request(ctx context.Context, method, path string, query url.Values, body, out any, authorize func(*http.Request), secrets ...string) error {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("forgejo base URL: %w", err)
	}
	// Joining rather than concatenating keeps a base URL that carries a path
	// prefix (https://host/forgejo) intact.
	target := base.JoinPath(path)
	target.RawQuery = query.Encode()

	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("forgejo request body for %s: %w", path, err)
		}
		payload = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, target.String(), payload)
	if err != nil {
		return fmt.Errorf("forgejo request for %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	authorize(req)

	res, err := c.hc.Do(req)
	if err != nil {
		// The text names the URL and the path. Neither carries the credential,
		// which travelled in a header.
		return fmt.Errorf("forgejo request for %s: %w", path, err)
	}
	defer func() { _ = res.Body.Close() }()

	// 2xx is the success band, not 200 alone: a delete answers 204.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &StatusError{Code: res.StatusCode, Message: redact(forgejoMessage(res.Body), secrets)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("forgejo response for %s: %w", path, err)
	}
	return nil
}

// forgejoMessage reads the one field of an error answer worth showing: Forgejo
// reports a failure as {"errors":…,"message":…,"url":…} and the message is the
// sentence meant for a human. A body that is not JSON, or is longer than a
// sentence should be, gives nothing rather than a wall.
func forgejoMessage(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 8<<10))
	if err != nil {
		return ""
	}
	var res struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return ""
	}
	return res.Message
}

// redact takes the secrets out of a string Forgejo wrote. It is the last line of
// R4, not the first: this client never puts a secret into a message, but an
// instance that echoes what it was sent would, and the wizard shows this text to
// whoever is looking at the screen.
func redact(message string, secrets []string) string {
	for _, s := range secrets {
		if s != "" {
			message = strings.ReplaceAll(message, s, "[redacted]")
		}
	}
	return message
}

// apiPath builds one of Forgejo's API paths from its segments, escaping the ones
// that come from a form field so a name with a slash or a space in it asks for
// what it means instead of a different path.
func apiPath(parts ...string) string {
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return "/api/v1/" + strings.Join(parts, "/")
}
