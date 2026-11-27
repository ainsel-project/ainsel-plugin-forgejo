package forgejo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The secrets these tests hand the client. They are long and distinctive so a
// leak into an error string or a %v is impossible to miss.
const (
	adminToken = "fj_admin_token_0123456789abcdef"
	botLogin   = "refiner-demo-slug"
	botPass    = "ZzBotPassword!23"
	botToken   = "fj_bot_token_0123456789abcdef"
)

// recordedRequest is what the fake Forgejo saw, so a test asserts on the call
// rather than only on the answer.
type recordedRequest struct {
	method string
	path   string
	query  url.Values
	auth   string
	ctype  string
	body   string

	// seen counts the requests, so a test can tell "one call" from "paged twice".
	seen int
}

// basicUser / basicPass decode the basic-auth credential the fake saw, empty when
// the request carried none.
func (r *recordedRequest) basic() (string, string) {
	const prefix = "Basic "
	if !strings.HasPrefix(r.auth, prefix) {
		return "", ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.auth, prefix))
	if err != nil {
		return "", ""
	}
	user, pass, _ := strings.Cut(string(raw), ":")
	return user, pass
}

// jsonBody parses the body the fake received. A test that asserts on a field
// rather than on the raw string says what the field is for.
func (r *recordedRequest) jsonBody(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, r.body)
	}
	return out
}

// stub answers every request with the given status and body, and records the last
// one it saw. responses, when given, is consumed one entry per request: request 1
// gets body, request 2 gets responses[0], request 3 gets responses[1], etc.
func stub(t *testing.T, status int, body string, responses ...string) (*Client, *recordedRequest) {
	t.Helper()

	rec := &recordedRequest{}
	responses = append([]string{}, responses...) // copy so we don't mutate the caller's slice
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.seen++
		if rec.seen == 1 {
			rec.method = r.Method
			rec.path = r.URL.Path
			rec.query = r.URL.Query()
			rec.auth = r.Header.Get("Authorization")
			rec.ctype = r.Header.Get("Content-Type")
			rec.body = string(raw)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		// Use staged response if we've moved past the first request.
		if rec.seen > 1 && len(responses) > 0 {
			body = responses[0]
			responses = responses[1:]
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return New(srv.URL, srv.Client()), rec
}

func admin(t *testing.T) (*Client, *recordedRequest) {
	t.Helper()
	c, rec := stub(t, http.StatusOK, `{}`)
	return c, rec
}

// TestNew checks the two things the client is built with: the address, and a
// client that gives up. A wizard step that hangs forever holds an admin's
// request and a background reconcile alike, so the ceiling is on the client.
func TestNew(t *testing.T) {
	c := New("https://forgejo.example/", nil)
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.hc == nil {
		t.Fatal("no http client")
	}
	if want := 10 * time.Second; c.hc.Timeout != want {
		t.Errorf("timeout = %s, want %s", c.hc.Timeout, want)
	}

	// A caller's own client with a longer timeout gets clamped to the 10 s ceiling.
	own := &http.Client{Timeout: time.Minute}
	got := New("https://forgejo.example", own)
	if got.hc.Timeout != 10*time.Second {
		t.Errorf("timeout = %s, want clamped to %s", got.hc.Timeout, 10*time.Second)
	}
	// But we keep the caller's http.Client pointer if it has no timeout or
	// a shorter one (not shown here, as New with nil or short timeout keeps own).
	own2 := &http.Client{Timeout: 5 * time.Second}
	if got2 := New("https://forgejo.example", own2); got2.hc != own2 {
		t.Error("New replaced the http client with a shorter timeout")
	}
}

// TestTrailingSlashBaseURL checks that a base URL written with or without a
// trailing slash reaches the same path — the address comes from a form field.
func TestTrailingSlashBaseURL(t *testing.T) {
	c, rec := stub(t, http.StatusOK, `{"login":"admin","is_admin":true}`)

	if _, _, err := New(c.baseURL+"/", nil).Admin(adminToken).WhoAmI(context.Background()); err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if want := "/api/v1/user"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
}

// call is one method of the client, run against the fake so the test can read
// over its shoulder.
type call struct {
	name   string
	run    func(c *Client) error
	method string
	path   string
	// wantQuery is checked against the query the request carried.
	wantQuery url.Values
	// wantBody holds the body fields the request has to carry, by dotted path.
	wantBody map[string]any
	// status / response stage the answer.
	status   int
	response string
}

func runCalls(t *testing.T, calls []call) {
	t.Helper()

	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}
			c, rec := stub(t, status, tc.response)

			if err := tc.run(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if rec.method != tc.method {
				t.Errorf("method = %q, want %q", rec.method, tc.method)
			}
			if rec.path != tc.path {
				t.Errorf("path = %q, want %q", rec.path, tc.path)
			}
			for k, want := range tc.wantQuery {
				if got := rec.query.Get(k); got != want[0] {
					t.Errorf("query %s = %q, want %q", k, got, want[0])
				}
			}
			// Every admin call carries the admin token and nothing else.
			if want := "token " + adminToken; rec.auth != want {
				t.Errorf("Authorization = %q, want %q", rec.auth, want)
			}
			if len(tc.wantBody) > 0 {
				body := rec.jsonBody(t)
				for k, want := range tc.wantBody {
					// DeepEqual, not ==: a body field can be an object (units_map),
					// and comparing two uncomparable values panics.
					if got := body[k]; !reflect.DeepEqual(got, want) {
						t.Errorf("body %s = %v, want %v", k, got, want)
					}
				}
				if rec.ctype != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", rec.ctype)
				}
			}
		})
	}
}

// TestAdminCalls pins the path, method and body of every call the admin token
// makes — the set the wizard's Apply and removal steps are built from.
func TestAdminCalls(t *testing.T) {
	ctx := context.Background()
	units := map[string]string{"repo.code": "read", "repo.issues": "write"}
	hook := Hook{URL: "https://hub.example/hooks/c/1", Secret: "whsec_123", Events: []string{"issues"}}

	runCalls(t, []call{
		{
			name:     "whoami",
			run:      func(c *Client) error { _, _, err := c.Admin(adminToken).WhoAmI(ctx); return err },
			method:   http.MethodGet,
			path:     "/api/v1/user",
			response: `{"login":"admin","is_admin":true}`,
		},
		{
			name:     "org exists",
			run:      func(c *Client) error { _, err := c.Admin(adminToken).OrgExists(ctx, "demo"); return err },
			method:   http.MethodGet,
			path:     "/api/v1/orgs/demo",
			response: `{"username":"demo"}`,
		},
		{
			name:     "user exists",
			run:      func(c *Client) error { _, err := c.Admin(adminToken).UserExists(ctx, botLogin); return err },
			method:   http.MethodGet,
			path:     "/api/v1/users/" + botLogin,
			response: `{"login":"` + botLogin + `"}`,
		},
		{
			name: "create user",
			run: func(c *Client) error {
				return c.Admin(adminToken).CreateUser(ctx, botLogin, "AInsel Refiner (demo)", botPass)
			},
			method:   http.MethodPost,
			path:     "/api/v1/admin/users",
			status:   http.StatusCreated,
			response: `{"login":"` + botLogin + `"}`,
			wantBody: map[string]any{
				"username":             botLogin,
				"full_name":            "AInsel Refiner (demo)",
				"password":             botPass,
				"must_change_password": false,
				"visibility":           "private",
			},
		},
		{
			name: "create team",
			run: func(c *Client) error {
				_, err := c.Admin(adminToken).CreateTeam(ctx, "demo", "ainsel-refiner-demo", units)
				return err
			},
			method:   http.MethodPost,
			path:     "/api/v1/orgs/demo/teams",
			status:   http.StatusCreated,
			response: `{"id":7}`,
			wantBody: map[string]any{
				"name":                      "ainsel-refiner-demo",
				"includes_all_repositories": true,
				"units_map":                 map[string]any{"repo.code": "read", "repo.issues": "write"},
			},
		},
		{
			name:   "add team member",
			run:    func(c *Client) error { return c.Admin(adminToken).AddTeamMember(ctx, 7, botLogin) },
			method: http.MethodPut,
			path:   "/api/v1/teams/7/members/" + botLogin,
			status: http.StatusNoContent,
		},
		{
			name:     "create org hook",
			run:      func(c *Client) error { _, err := c.Admin(adminToken).CreateOrgHook(ctx, "demo", hook); return err },
			method:   http.MethodPost,
			path:     "/api/v1/orgs/demo/hooks",
			status:   http.StatusCreated,
			response: `{"id":9}`,
			wantBody: map[string]any{
				"type":   "forgejo",
				"active": true,
			},
		},
		{
			name:     "update org hook",
			run:      func(c *Client) error { return c.Admin(adminToken).UpdateOrgHook(ctx, "demo", 9, hook) },
			method:   http.MethodPatch,
			path:     "/api/v1/orgs/demo/hooks/9",
			response: `{"id":9}`,
			wantBody: map[string]any{
				"type":   "forgejo",
				"active": true,
			},
		},
		{
			name:   "delete org hook",
			run:    func(c *Client) error { return c.Admin(adminToken).DeleteOrgHook(ctx, "demo", 9) },
			method: http.MethodDelete,
			path:   "/api/v1/orgs/demo/hooks/9",
			status: http.StatusNoContent,
		},
		{
			name:      "delete user purges",
			run:       func(c *Client) error { return c.Admin(adminToken).DeleteUser(ctx, botLogin) },
			method:    http.MethodDelete,
			path:      "/api/v1/admin/users/" + botLogin,
			wantQuery: url.Values{"purge": {"true"}},
			status:    http.StatusNoContent,
		},
		{
			name:   "delete team",
			run:    func(c *Client) error { return c.Admin(adminToken).DeleteTeam(ctx, 7) },
			method: http.MethodDelete,
			path:   "/api/v1/teams/7",
			status: http.StatusNoContent,
		},
	})
}

// TestHookBody checks the shape of the webhook body Forgejo wants: the URL, the
// secret and the content type travel inside config, the events beside it.
func TestHookBody(t *testing.T) {
	ctx := context.Background()
	events := []string{"issues", "issue_comment", "pull_request", "pull_request_review"}

	c, rec := stub(t, http.StatusCreated, `{"id":9}`)
	if _, err := c.Admin(adminToken).CreateOrgHook(ctx, "demo", Hook{
		URL: "https://hub.example/hooks/c/1", Secret: "whsec_123", Events: events,
	}); err != nil {
		t.Fatalf("CreateOrgHook: %v", err)
	}

	body := rec.jsonBody(t)
	config, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("body config = %v, want an object", body["config"])
	}
	for k, want := range map[string]string{
		"url":          "https://hub.example/hooks/c/1",
		"secret":       "whsec_123",
		"content_type": "json",
	} {
		if config[k] != want {
			t.Errorf("config[%s] = %v, want %q", k, config[k], want)
		}
	}
	got, _ := body["events"].([]any)
	if len(got) != len(events) {
		t.Fatalf("events = %v, want %v", got, events)
	}
	for i, want := range events {
		if got[i] != want {
			t.Errorf("events[%d] = %v, want %q", i, got[i], want)
		}
	}
}

// TestWhoAmIReadsTheAnswer checks that the login and the admin flag come back out
// of the body Forgejo answers with — the preview blocks on exactly these two.
func TestWhoAmIReadsTheAnswer(t *testing.T) {
	c, _ := stub(t, http.StatusOK, `{"login":"admin","is_admin":true}`)

	login, isAdmin, err := c.Admin(adminToken).WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if login != "admin" {
		t.Errorf("login = %q, want %q", login, "admin")
	}
	if !isAdmin {
		t.Error("isAdmin = false, want true")
	}
}

// TestExistsChecksSayNoOn404 checks the three existence probes: a 404 is the
// answer that means "the name is free", not an error. A taken login blocks the
// wizard; a 404 does not.
func TestExistsChecksSayNoOn404(t *testing.T) {
	ctx := context.Background()
	c, _ := stub(t, http.StatusNotFound, `{"message":"not found"}`)
	a := c.Admin(adminToken)

	if got, err := a.OrgExists(ctx, "demo"); got || err != nil {
		t.Errorf("OrgExists = %v, %v; want false, nil", got, err)
	}
	if got, err := a.UserExists(ctx, botLogin); got || err != nil {
		t.Errorf("UserExists = %v, %v; want false, nil", got, err)
	}
	// TeamExists gets a 404 on the list call, which becomes "no such team"
	// (both the org and a team inside it count as "free" from the preview's angle).
	if got, err := a.TeamExists(ctx, "demo", "ainsel-refiner-demo"); got || err != nil {
		t.Errorf("TeamExists = %v, %v; want false, nil", got, err)
	}
}

// TestTeamExistsReadsTheNames checks that the team probe matches the name exactly
// and keeps paging: a team on a later page is still taken, and a preview that
// called it free would have the wizard try to create a duplicate.
func TestTeamExistsReadsTheNames(t *testing.T) {
	ctx := context.Background()
	page1 := `[{"name":"owners"},{"name":"devs"}]`
	page2 := `[{"name":"ainsel-refiner-demo"}]`

	c, rec := stub(t, http.StatusOK, page1, page2, `[]`)
	got, err := c.Admin(adminToken).TeamExists(ctx, "demo", "ainsel-refiner-demo")
	if err != nil {
		t.Fatalf("TeamExists: %v", err)
	}
	if !got {
		t.Error("TeamExists = false, want true (the name is on the second page)")
	}
	if rec.seen < 2 {
		t.Errorf("TeamExists made %d request(s), want ≥2 (paging to find the team)", rec.seen)
	}

	// And a name that is nowhere in the org is free, in spite of a prefix match.
	c2, rec2 := stub(t, http.StatusOK, `[{"name":"ainsel-refiner-demo-other"}]`, `[]`)
	free, err := c2.Admin(adminToken).TeamExists(ctx, "demo", "ainsel-refiner-demo")
	if err != nil {
		t.Fatalf("TeamExists: %v", err)
	}
	if free {
		t.Error("TeamExists = true for a name that only shares a prefix")
	}
	if rec2.query.Get("limit") == "" {
		t.Error("no page size on the team list")
	}
}

// TestMintTokenUsesBasicAuth checks V8: the mint goes over basic auth as the bot,
// with the one-time password, and not as the admin — Forgejo mints a user's token
// only for that user, and an admin Sudo mint answers 401.
func TestMintTokenUsesBasicAuth(t *testing.T) {
	ctx := context.Background()
	c, rec := stub(t, http.StatusCreated, `{"id":3,"name":"ainsel","sha1":"`+botToken+`"}`)

	got, err := c.MintToken(ctx, botLogin, botPass, "ainsel", []string{"write:issue", "read:user"})
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if got != botToken {
		t.Errorf("token = %q, want %q", got, botToken)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/users/"+botLogin+"/tokens" {
		t.Errorf("call = %s %s, want POST /api/v1/users/%s/tokens", rec.method, rec.path, botLogin)
	}

	user, pass := rec.basic()
	if user != botLogin || pass != botPass {
		t.Errorf("basic auth = %q/%q, want %q and the one-time password", user, pass, botLogin)
	}
	// No admin token on this one — it would mint for the admin, not for the bot.
	if strings.HasPrefix(rec.auth, "token ") || strings.Contains(rec.auth, adminToken) {
		t.Errorf("Authorization = %q, want basic auth only", rec.auth)
	}

	body := rec.jsonBody(t)
	if body["name"] != "ainsel" {
		t.Errorf("body name = %v, want %q", body["name"], "ainsel")
	}
	scopes, _ := body["scopes"].([]any)
	if len(scopes) != 2 || scopes[0] != "write:issue" || scopes[1] != "read:user" {
		t.Errorf("body scopes = %v, want [write:issue read:user]", body["scopes"])
	}
}

// TestMintTokenWithoutAToken checks the failure a caller cannot ignore: a 201
// whose body carries no token is not a minted token.
func TestMintTokenWithoutAToken(t *testing.T) {
	c, _ := stub(t, http.StatusCreated, `{"id":3,"name":"ainsel"}`)

	if _, err := c.MintToken(context.Background(), botLogin, botPass, "ainsel", nil); err == nil {
		t.Fatal("MintToken succeeded, want an error: the answer carried no token")
	}
}

// TestStatusError checks that a refusal comes back as a *StatusError carrying
// both the code and Forgejo's own message: "the token is not an admin" and "that
// login is taken" are different answers, and the wizard says them to the admin.
func TestStatusError(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		status  int
		message string
	}{
		{"401", http.StatusUnauthorized, "invalid credentials"},
		{"422", http.StatusUnprocessableEntity, "name is already taken"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := stub(t, tc.status, `{"errors":null,"message":"`+tc.message+`","url":"http://fj/api/v1/docs"}`)

			_, _, err := c.Admin(adminToken).WhoAmI(ctx)
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("error = %v, want a *StatusError", err)
			}
			if se.Code != tc.status {
				t.Errorf("Code = %d, want %d", se.Code, tc.status)
			}
			if se.Message != tc.message {
				t.Errorf("Message = %q, want %q", se.Message, tc.message)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error = %q, want it to name %q", err.Error(), tc.message)
			}
		})
	}
}

// TestSecretsNeverInErrors is R4 at the client: whatever goes wrong, the text a
// caller gets back names the failure without repeating a token or a password —
// the wizard shows these strings to whoever is looking at the screen, and a
// rollback writes them into a log.
func TestSecretsNeverInErrors(t *testing.T) {
	ctx := context.Background()

	// A refusal on an admin call: the admin token must not come back in it, even
	// though the request carried it.
	c, _ := stub(t, http.StatusUnauthorized, `{"message":"invalid credentials"}`)
	if _, _, err := c.Admin(adminToken).WhoAmI(ctx); err == nil {
		t.Fatal("WhoAmI succeeded, want the 401")
	} else if strings.Contains(err.Error(), adminToken) {
		t.Errorf("error = %q, want no admin token in it", err)
	}

	// A 422 on the user create, where the request body carried the password.
	c2, _ := stub(t, http.StatusUnprocessableEntity, `{"message":"name is already taken"}`)
	if err := c2.Admin(adminToken).CreateUser(ctx, botLogin, "AInsel Refiner (demo)", botPass); err == nil {
		t.Fatal("CreateUser succeeded, want the 422")
	} else if strings.Contains(err.Error(), botPass) || strings.Contains(err.Error(), adminToken) {
		t.Errorf("error = %q, want no secret in it", err)
	}

	// The basic-auth mint refused: neither the password nor the login's credential.
	c3, _ := stub(t, http.StatusUnauthorized, `{"message":"two-factor required"}`)
	if _, err := c3.MintToken(ctx, botLogin, botPass, "ainsel", nil); err == nil {
		t.Fatal("MintToken succeeded, want the 401")
	} else if strings.Contains(err.Error(), botPass) {
		t.Errorf("error = %q, want no password in it", err)
	}
}

// TestSecretsAreRedactedFromForgejosOwnMessage closes the hole the previous test
// cannot: an instance that echoes what it was sent. Whatever Forgejo puts in its
// message, a secret the client handed over does not survive into the error.
func TestSecretsAreRedactedFromForgejosOwnMessage(t *testing.T) {
	ctx := context.Background()
	echoed := `{"message":"password ` + botPass + ` and token ` + adminToken + ` are invalid"}`

	c, _ := stub(t, http.StatusUnprocessableEntity, echoed)
	err := c.Admin(adminToken).CreateUser(ctx, botLogin, "AInsel Refiner (demo)", botPass)
	if err == nil {
		t.Fatal("CreateUser succeeded, want the 422")
	}
	if strings.Contains(err.Error(), botPass) || strings.Contains(err.Error(), adminToken) {
		t.Errorf("error = %q, want both secrets redacted", err)
	}
	if !strings.Contains(err.Error(), "are invalid") {
		t.Errorf("error = %q, want the rest of Forgejo's message kept", err)
	}
}

// TestHookSecretIsRedacted covers the third secret this client carries: the
// webhook secret, which arrives straight from the connector create and is sent
// inside the hook body. A refused create or patch must not hand it back — the
// re-apply loop writes what it gets out of here into a log line.
func TestHookSecretIsRedacted(t *testing.T) {
	ctx := context.Background()
	echoed := `{"message":"config url and secret whsec_123 are not valid"}`
	hook := Hook{URL: "https://hub.example/hooks/c/1", Secret: "whsec_123", Events: []string{"issues"}}

	for _, tc := range []struct {
		name string
		run  func(a *Admin) error
	}{
		{"create", func(a *Admin) error { _, err := a.CreateOrgHook(ctx, "demo", hook); return err }},
		{"update", func(a *Admin) error { return a.UpdateOrgHook(ctx, "demo", 9, hook) }},
	} {
		c, _ := stub(t, http.StatusUnprocessableEntity, echoed)
		err := tc.run(c.Admin(adminToken))
		if err == nil {
			t.Fatalf("hook %s succeeded, want the 422", tc.name)
		}
		if strings.Contains(err.Error(), "whsec_123") {
			t.Errorf("hook %s error = %q, want the webhook secret redacted", tc.name, err)
		}
		if !strings.Contains(err.Error(), "are not valid") {
			t.Errorf("hook %s error = %q, want the rest of Forgejo's message kept", tc.name, err)
		}
	}
}

// TestAdminDoesNotPrintItsToken checks the same rule through %v: an Admin that
// ends up in a log line or a fmt of a surrounding struct says who it is without
// handing out the credential.
func TestAdminDoesNotPrintItsToken(t *testing.T) {
	c, _ := stub(t, http.StatusOK, `{}`)
	a := c.Admin(adminToken)

	for _, s := range []string{
		a.String(),
		// The string-verb path: the fmt.Sprintf("%s", a) case staticcheck's
		// S1025 flagged as redundant lived here. fmt.Sprint keeps the guard —
		// with only a String() method on Admin, Sprint renders exactly what
		// %s rendered — without the redundancy the linter objected to.
		fmt.Sprint(a),
		fmt.Sprintf("%v", a),
		fmt.Sprintf("%+v", struct {
			Admin *Admin
			Step  string
		}{a, "create bots"}),
	} {
		if strings.Contains(s, adminToken) {
			t.Errorf("%q contains the token", s)
		}
	}
	if !strings.HasPrefix(a.String(), "forgejo.Admin{") {
		t.Errorf("String() = %q, want it to name the type", a.String())
	}
}

// TestHookDoesNotPrintItsSecret checks the webhook secret, which the client is
// handed straight from the connector create and must never repeat.
func TestHookDoesNotPrintItsSecret(t *testing.T) {
	h := Hook{URL: "https://hub.example/hooks/c/1", Secret: "whsec_123", Events: []string{"issues"}}

	for _, s := range []string{h.String(), fmt.Sprintf("%v", h), fmt.Sprintf("%+v", h)} {
		if strings.Contains(s, "whsec_123") {
			t.Errorf("%q contains the webhook secret", s)
		}
	}
}

// TestCallsRefuseACancelledContext checks that a call does not outlive its
// context: a wizard request the admin walked away from stops.
func TestCancelledContext(t *testing.T) {
	c, _ := admin(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := c.Admin(adminToken).WhoAmI(ctx); err == nil {
		t.Fatal("WhoAmI on a cancelled context succeeded, want an error")
	}
}

// -- the orphan sweep's reads -------------------------------------------------------

// TestFindTeamKeepsTheID: the sweep needs the id a delete takes, which is the
// whole difference between FindTeam and TeamExists.
func TestFindTeamKeepsTheID(t *testing.T) {
	c, rec := stub(t, http.StatusOK, `[{"id":7,"name":"ainsel-developer-demo"},{"id":8,"name":"ainsel-refiner-demo"}]`)

	id, found, err := c.Admin(adminToken).FindTeam(context.Background(), "demo-org", "ainsel-refiner-demo")
	if err != nil {
		t.Fatalf("FindTeam: %v", err)
	}
	if !found {
		t.Fatal("found = false, want the team on the first page")
	}
	if id != 8 {
		t.Errorf("id = %d, want 8", id)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/orgs/demo-org/teams" {
		t.Errorf("call = %s %s, want GET /api/v1/orgs/demo-org/teams", rec.method, rec.path)
	}
}

// TestFindTeamReadsThePages: a team on a later page is still the team — the list
// is read to its end, the way TeamExists reads it.
func TestFindTeamReadsThePages(t *testing.T) {
	c, rec := stub(t, http.StatusOK,
		`[{"id":1,"name":"some-other-team"}]`,
		`[{"id":42,"name":"ainsel-reviewer-demo"}]`,
	)

	id, found, err := c.Admin(adminToken).FindTeam(context.Background(), "demo-org", "ainsel-reviewer-demo")
	if err != nil {
		t.Fatalf("FindTeam: %v", err)
	}
	if !found || id != 42 {
		t.Errorf("found = (%t, %d), want (true, 42)", found, id)
	}
	if rec.seen != 2 {
		t.Errorf("pages read = %d, want 2", rec.seen)
	}
}

// TestFindTeamSaysNoForAFreeName: not found is an answer, not an error.
func TestFindTeamSaysNoForAFreeName(t *testing.T) {
	c, _ := stub(t, http.StatusOK,
		`[{"id":1,"name":"ainsel-refiner-other"}]`,
		`[]`,
	)

	_, found, err := c.Admin(adminToken).FindTeam(context.Background(), "demo-org", "ainsel-refiner-demo")
	if err != nil {
		t.Fatalf("FindTeam: %v", err)
	}
	if found {
		t.Error("found = true, want false: an exact-name match only")
	}
}

// TestListOrgHooks reads the org's webhooks with the fields the sweep names a
// candidate by: the id, and the URL it points at.
func TestListOrgHooks(t *testing.T) {
	body := `[{"id":3,"type":"forgejo","active":true,"events":["issues"],` +
		`"config":{"url":"https://hub.test/api/v1/connectors/c-9/webhook","secret":"should-not-be-read"}}]`
	c, rec := stub(t, http.StatusOK, body)

	hooks, err := c.Admin(adminToken).ListOrgHooks(context.Background(), "demo-org")
	if err != nil {
		t.Fatalf("ListOrgHooks: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(hooks))
	}
	h := hooks[0]
	if h.ID != 3 || h.Type != "forgejo" || !h.Active {
		t.Errorf("hook = %+v, want id 3, type forgejo, active", h)
	}
	if h.URL != "https://hub.test/api/v1/connectors/c-9/webhook" {
		t.Errorf("url = %q, want the connector endpoint", h.URL)
	}
	if len(h.Events) != 1 || h.Events[0] != "issues" {
		t.Errorf("events = %v, want [issues]", h.Events)
	}
	if rec.path != "/api/v1/orgs/demo-org/hooks" {
		t.Errorf("path = %q, want the org hooks list", rec.path)
	}
}

// -- the 404-tolerant deletes --------------------------------------------------------

// TestDeletesTolerateGoneResources: a retried removal re-attempts what an earlier
// pass already deleted. "Not there" is the goal met — otherwise a row kept for a
// leftover could never clear, and the removal would never converge.
func TestDeletesTolerateGoneResources(t *testing.T) {
	ctx := context.Background()

	t.Run("404 is deleted", func(t *testing.T) {
		c, _ := stub(t, http.StatusNotFound, `{"message":"not found"}`)
		admin := c.Admin(adminToken)
		if err := admin.DeleteUser(ctx, botLogin); err != nil {
			t.Errorf("DeleteUser: %v, want nil", err)
		}
		if err := admin.DeleteTeam(ctx, 7); err != nil {
			t.Errorf("DeleteTeam: %v, want nil", err)
		}
		if err := admin.DeleteOrgHook(ctx, "demo-org", 3); err != nil {
			t.Errorf("DeleteOrgHook: %v, want nil", err)
		}
	})

	t.Run("anything else is still an error", func(t *testing.T) {
		c, _ := stub(t, http.StatusInternalServerError, `{"message":"boom"}`)
		admin := c.Admin(adminToken)
		if err := admin.DeleteUser(ctx, botLogin); err == nil {
			t.Error("DeleteUser on a 500: nil error, want the failure")
		}
		if err := admin.DeleteTeam(ctx, 7); err == nil {
			t.Error("DeleteTeam on a 500: nil error, want the failure")
		}
		if err := admin.DeleteOrgHook(ctx, "demo-org", 3); err == nil {
			t.Error("DeleteOrgHook on a 500: nil error, want the failure")
		}
	})
}
