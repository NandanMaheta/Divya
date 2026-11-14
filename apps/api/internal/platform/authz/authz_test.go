// Tests for the fail-closed authorisation middleware (T004; FR-030..FR-035, plan P-09, C-8).
//
// API assumptions (contracts are silent, written down so the implementer can match them):
//   - Errors are RFC 9457 JSON (application/problem+json) with a top-level "code" member. authz tests
//     do not call httpx.InstallErrors, so authz must put "code" in its own responses.
//   - Install registers securitySchemes "bearer" (http/bearer/JWT) and "mfaChallenge"
//     (http/bearer/opaque) in api.OpenAPI().Components, and routes each scheme name to the
//     Verifier of the same name in the map. A scheme with no verifier is unknown.
//   - "Panics in Install/Register" is checked as: registering the operation and then calling
//     AssertDeclared panics (the implementation may panic earlier, inside Register, via an
//     OnAddOperation hook; the tests accept either place).
//   - Registering an operation with nil Security, or SelfAuthenticated() on a documented
//     operation, does NOT panic at Register time (the runtime tests need to reach the 500 backstop);
//     AssertDeclared is what panics.
//   - SelfAuthenticated() returns only the Security slice, so callers also set
//     Metadata["authz"]="self" on the operation (the contract says "plus Metadata"); the tests do both.
//   - Handlers return nothing the middleware inspects; "handler ran" is a counter.
package authz_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/authz"
)

// ---- fakes ----

type fakeVerifier struct {
	mu         sync.Mutex
	principals map[string]authz.Principal
	calls      []string
}

func (f *fakeVerifier) Verify(_ context.Context, token string) (authz.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, token)
	if p, ok := f.principals[token]; ok {
		return p, nil
	}
	return authz.Principal{}, errors.New("rejected")
}

func (f *fakeVerifier) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

var (
	adminP = authz.Principal{
		UserID: uuid.MustParse("00000000-0000-7000-8000-0000000000a1"), OrgID: uuid.MustParse("00000000-0000-7000-8000-0000000000b1"),
		SessionID: uuid.MustParse("00000000-0000-7000-8000-0000000000c1"), Role: authz.RoleAdmin,
	}
	clientP = authz.Principal{
		UserID: uuid.MustParse("00000000-0000-7000-8000-0000000000a2"), OrgID: uuid.MustParse("00000000-0000-7000-8000-0000000000b1"),
		SessionID: uuid.MustParse("00000000-0000-7000-8000-0000000000c2"), Role: authz.RoleClient,
	}
	// An mfaChallenge principal has no session (go-capabilities section 1).
	challengeP = authz.Principal{UserID: adminP.UserID, OrgID: adminP.OrgID, SessionID: uuid.Nil, Role: authz.RoleAdmin}
)

type env struct {
	api       humatest.TestAPI
	bearer    *fakeVerifier
	challenge *fakeVerifier
	ran       int
	got       authz.Principal
	gotOK     bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		bearer:    &fakeVerifier{principals: map[string]authz.Principal{"admin-token": adminP, "client-token": clientP}},
		challenge: &fakeVerifier{principals: map[string]authz.Principal{"dmc_good": challengeP}},
	}
	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))
	authz.Install(api, map[string]authz.Verifier{"bearer": e.bearer, "mfaChallenge": e.challenge})
	e.api = api
	return e
}

type empty struct{}

type okOut struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

func (e *env) handler(_ context.Context, _ *empty) (*okOut, error) {
	e.ran++
	out := &okOut{}
	out.Body.OK = true
	return out, nil
}

func (e *env) principalHandler(ctx context.Context, _ *empty) (*okOut, error) {
	e.ran++
	e.got, e.gotOK = authz.PrincipalFrom(ctx)
	out := &okOut{}
	out.Body.OK = true
	return out, nil
}

func (e *env) register(op huma.Operation) {
	if op.Method == "" {
		op.Method = http.MethodGet
	}
	huma.Register(e.api, op, e.handler)
}

func bearer(token string) string { return "Authorization: Bearer " + token }

type problem struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) problem {
	t.Helper()
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return p
}

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected a panic", what)
		}
	}()
	f()
}

func mustNotPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: unexpected panic: %v", what, r)
		}
	}()
	f()
}

// ---- declaration helpers ----

func TestDeclarations_Shapes(t *testing.T) {
	if got := authz.Public(); got == nil || len(got) != 0 {
		t.Errorf("Public() = %#v, want a non-nil empty slice so Huma serialises security: []", got)
	}
	if got := authz.SelfAuthenticated(); got == nil || len(got) != 0 {
		t.Errorf("SelfAuthenticated() = %#v, want a non-nil empty slice", got)
	}

	signedIn := authz.Require()
	if len(signedIn) != 1 || len(signedIn[0]) != 1 {
		t.Fatalf("Require() = %#v, want one requirement with one scheme", signedIn)
	}
	if scopes, ok := signedIn[0]["bearer"]; !ok || len(scopes) != 0 {
		t.Errorf("Require() = %#v, want [{bearer: []}]", signedIn)
	}

	admin := authz.Require(authz.RoleAdmin)
	if len(admin) != 1 || len(admin[0]["bearer"]) != 1 || admin[0]["bearer"][0] != "admin" {
		t.Errorf("Require(RoleAdmin) = %#v, want [{bearer: [admin]}]", admin)
	}

	both := authz.Require(authz.RoleAdmin, authz.RoleClient)
	if len(both) != 1 || len(both[0]["bearer"]) != 2 {
		t.Errorf("Require(admin, client) = %#v, want one requirement with both roles", both)
	}

	ch := authz.ChallengeOnly()
	if len(ch) != 1 || len(ch[0]) != 1 {
		t.Fatalf("ChallengeOnly() = %#v", ch)
	}
	if scopes, ok := ch[0]["mfaChallenge"]; !ok || len(scopes) != 0 {
		t.Errorf("ChallengeOnly() = %#v, want [{mfaChallenge: []}]", ch)
	}

	if authz.RoleAdmin != "admin" || authz.RoleClient != "client" {
		t.Errorf("role values must be \"admin\" and \"client\", got %q and %q", authz.RoleAdmin, authz.RoleClient)
	}
}

func TestInstall_RegistersSecuritySchemes(t *testing.T) {
	e := newEnv(t)
	schemes := e.api.OpenAPI().Components.SecuritySchemes
	b, ok := schemes["bearer"]
	if !ok {
		t.Fatalf("securitySchemes lacks bearer: %v", schemes)
	}
	if b.Type != "http" || b.Scheme != "bearer" || b.BearerFormat != "JWT" {
		t.Errorf("bearer scheme = %+v, want http/bearer/JWT", b)
	}
	m, ok := schemes["mfaChallenge"]
	if !ok {
		t.Fatalf("securitySchemes lacks mfaChallenge: %v", schemes)
	}
	if m.Type != "http" || m.Scheme != "bearer" || m.BearerFormat != "opaque" {
		t.Errorf("mfaChallenge scheme = %+v, want http/bearer/opaque", m)
	}
}

// ---- fail-closed on undeclared Security ----

func TestMiddleware_NilSecurityAnswers500_Documented(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "undeclared", Path: "/undeclared"})
	for name, hdr := range map[string][]any{
		"no credentials":      nil,
		"valid admin token":   {bearer("admin-token")},
		"garbage credentials": {"Authorization: nonsense"},
	} {
		t.Run(name, func(t *testing.T) {
			before := e.ran
			rec := e.api.Get("/undeclared", hdr...)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			if p := decode(t, rec); p.Code != "platform.security_undeclared" {
				t.Errorf("code = %q, want platform.security_undeclared", p.Code)
			}
			if e.ran != before {
				t.Error("handler ran for an operation with undeclared Security")
			}
		})
	}
}

func TestMiddleware_NilSecurityAnswers500_Hidden(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "hidden-undeclared", Path: "/hidden-undeclared", Hidden: true})
	rec := e.api.Get("/hidden-undeclared")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (hidden operations are not exempt)", rec.Code)
	}
	if p := decode(t, rec); p.Code != "platform.security_undeclared" {
		t.Errorf("code = %q, want platform.security_undeclared", p.Code)
	}
	if e.ran != 0 {
		t.Error("handler ran")
	}
}

func TestMiddleware_EmptySecurityPasses(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "public", Path: "/public", Security: authz.Public()})
	for name, hdr := range map[string][]any{
		"no credentials":    nil,
		"invalid bearer":    {bearer("nope")},
		"valid bearer":      {bearer("admin-token")},
		"non bearer scheme": {"Authorization: Basic Zm9vOmJhcg=="},
	} {
		t.Run(name, func(t *testing.T) {
			before := e.ran
			rec := e.api.Get("/public", hdr...)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if e.ran != before+1 {
				t.Error("handler did not run")
			}
		})
	}
	if calls := e.bearer.called(); len(calls) != 0 {
		t.Errorf("a Public operation consulted the bearer verifier with %v", calls)
	}
}

func TestMiddleware_PublicOperationHasNoPrincipal(t *testing.T) {
	e := newEnv(t)
	huma.Register(e.api, huma.Operation{OperationID: "public", Method: http.MethodGet, Path: "/public", Security: authz.Public()}, e.principalHandler)
	if rec := e.api.Get("/public", bearer("admin-token")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if e.gotOK {
		t.Errorf("a Public operation must not receive a principal, got %+v", e.got)
	}
}

// ---- authentication ----

func TestMiddleware_Unauthenticated(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "any", Path: "/any", Security: authz.Require()})
	tests := []struct {
		name string
		hdr  []any
	}{
		{"missing header", nil},
		{"empty header", []any{"Authorization: "}},
		{"basic scheme", []any{"Authorization: Basic Zm9vOmJhcg=="}},
		{"bearer without token", []any{"Authorization: Bearer"}},
		{"bearer with empty token", []any{"Authorization: Bearer "}},
		{"token without scheme", []any{"Authorization: admin-token"}},
		{"unknown token", []any{bearer("not-a-token")}},
		{"tampered token", []any{bearer("admin-token" + "x")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.api.Get("/any", tt.hdr...)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
			}
			if p := decode(t, rec); p.Code != "auth.unauthenticated" {
				t.Errorf("code = %q, want auth.unauthenticated", p.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want it to start with Bearer", got)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
		})
	}
	if e.ran != 0 {
		t.Error("handler ran for an unauthenticated request")
	}
}

func TestMiddleware_VerifierReceivesTokenWithoutScheme(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "any", Path: "/any", Security: authz.Require()})
	if rec := e.api.Get("/any", bearer("client-token")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if calls := e.bearer.called(); len(calls) != 1 || calls[0] != "client-token" {
		t.Errorf("verifier calls = %v, want exactly [client-token]", calls)
	}
}

func TestMiddleware_PrincipalReachesHandler(t *testing.T) {
	e := newEnv(t)
	huma.Register(e.api, huma.Operation{OperationID: "any", Method: http.MethodGet, Path: "/any", Security: authz.Require()}, e.principalHandler)
	rec := e.api.Get("/any", bearer("client-token"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !e.gotOK || e.got != clientP {
		t.Errorf("PrincipalFrom = %+v, %v; want %+v, true", e.got, e.gotOK, clientP)
	}
}

func TestMiddleware_MustPrincipalInsideRequire(t *testing.T) {
	e := newEnv(t)
	var got authz.Principal
	huma.Register(e.api, huma.Operation{OperationID: "must", Method: http.MethodGet, Path: "/must", Security: authz.Require(authz.RoleAdmin)},
		func(ctx context.Context, _ *empty) (*okOut, error) {
			got = authz.MustPrincipal(ctx)
			return &okOut{}, nil
		})
	if rec := e.api.Get("/must", bearer("admin-token")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got != adminP {
		t.Errorf("MustPrincipal = %+v, want %+v", got, adminP)
	}
}

func TestPrincipalFrom_AbsentAndMustPrincipalPanics(t *testing.T) {
	if p, ok := authz.PrincipalFrom(context.Background()); ok || p != (authz.Principal{}) {
		t.Errorf("PrincipalFrom(empty ctx) = %+v, %v; want zero, false", p, ok)
	}
	mustPanic(t, "MustPrincipal on a bare context", func() { authz.MustPrincipal(context.Background()) })
}

// ---- authorisation (roles) ----

func TestMiddleware_RoleScopes(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "admin-only", Path: "/admin", Security: authz.Require(authz.RoleAdmin)})
	e.register(huma.Operation{OperationID: "client-only", Path: "/client", Security: authz.Require(authz.RoleClient)})
	e.register(huma.Operation{OperationID: "either", Path: "/either", Security: authz.Require(authz.RoleAdmin, authz.RoleClient)})
	e.register(huma.Operation{OperationID: "signed-in", Path: "/signed-in", Security: authz.Require()})

	tests := []struct {
		path, token string
		want        int
	}{
		{"/admin", "admin-token", http.StatusOK},
		{"/admin", "client-token", http.StatusForbidden},
		{"/client", "client-token", http.StatusOK},
		{"/client", "admin-token", http.StatusForbidden},
		{"/either", "admin-token", http.StatusOK},
		{"/either", "client-token", http.StatusOK},
		{"/signed-in", "admin-token", http.StatusOK},
		{"/signed-in", "client-token", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.path+" "+tt.token, func(t *testing.T) {
			before := e.ran
			rec := e.api.Get(tt.path, bearer(tt.token))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.want == http.StatusForbidden {
				if p := decode(t, rec); p.Code != "auth.forbidden" {
					t.Errorf("code = %q, want auth.forbidden", p.Code)
				}
				if e.ran != before {
					t.Error("handler ran for a forbidden caller")
				}
			} else if e.ran != before+1 {
				t.Error("handler did not run for an allowed caller")
			}
		})
	}
}

func TestMiddleware_UnauthenticatedBeatsForbidden(t *testing.T) {
	// A bad token on an admin operation is 401, never 403 (no role information is leaked).
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "admin-only", Path: "/admin", Security: authz.Require(authz.RoleAdmin)})
	if rec := e.api.Get("/admin", bearer("nope")); rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// ---- challenge scheme ----

func TestMiddleware_ChallengeOnlyUsesChallengeVerifier(t *testing.T) {
	e := newEnv(t)
	huma.Register(e.api, huma.Operation{OperationID: "mfa", Method: http.MethodGet, Path: "/mfa", Security: authz.ChallengeOnly()}, e.principalHandler)

	rec := e.api.Get("/mfa", bearer("dmc_good"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !e.gotOK || e.got != challengeP || e.got.SessionID != uuid.Nil {
		t.Errorf("principal = %+v, %v; want the challenge principal with a nil SessionID", e.got, e.gotOK)
	}
	if calls := e.bearer.called(); len(calls) != 0 {
		t.Errorf("the bearer verifier was consulted for a challenge operation: %v", calls)
	}

	// An access token (valid for the bearer scheme) is not a challenge (FR-035).
	before := e.ran
	rec = e.api.Get("/mfa", bearer("admin-token"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("access token on a challenge operation: status = %d, want 401", rec.Code)
	}
	if p := decode(t, rec); p.Code != "auth.unauthenticated" {
		t.Errorf("code = %q, want auth.unauthenticated", p.Code)
	}
	if e.ran != before {
		t.Error("handler ran")
	}
}

func TestMiddleware_ChallengeTokenIsNotAnAccessToken(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "admin-only", Path: "/admin", Security: authz.Require(authz.RoleAdmin)})
	rec := e.api.Get("/admin", bearer("dmc_good"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("challenge token on a bearer operation: status = %d, want 401", rec.Code)
	}
	if calls := e.challenge.called(); len(calls) != 0 {
		t.Errorf("the challenge verifier was consulted for a bearer operation: %v", calls)
	}
}

// ---- self-authenticated hidden operations (C-8) ----

func TestMiddleware_SelfAuthenticatedHiddenPassesThrough(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{
		OperationID: "tick", Method: http.MethodGet, Path: "/internal/tick", Hidden: true,
		Security: authz.SelfAuthenticated(), Metadata: map[string]any{"authz": "self"},
	})
	rec := e.api.Get("/internal/tick") // no credentials: the handler authenticates itself
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (handler is reached): %s", rec.Code, rec.Body.String())
	}
	if e.ran != 1 {
		t.Error("handler did not run")
	}
	if calls := e.bearer.called(); len(calls) != 0 {
		t.Errorf("verifier consulted for a self-authenticated operation: %v", calls)
	}
}

func TestMiddleware_SelfAuthenticatedDocumentedAnswers500(t *testing.T) {
	e := newEnv(t)
	mustNotPanic(t, "Register of a documented SelfAuthenticated operation", func() {
		e.register(huma.Operation{
			OperationID: "bad", Path: "/bad",
			Security: authz.SelfAuthenticated(), Metadata: map[string]any{"authz": "self"},
		})
	})
	rec := e.api.Get("/bad")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (SelfAuthenticated is valid only on Hidden operations)", rec.Code)
	}
	if e.ran != 0 {
		t.Error("handler ran")
	}
}

// ---- ordering ----

func TestMiddleware_RunsBeforeBodyValidation(t *testing.T) {
	e := newEnv(t)
	type in struct {
		Body struct {
			Name string `json:"name" minLength:"3"`
		}
	}
	ran := 0
	huma.Register(e.api, huma.Operation{OperationID: "create", Method: http.MethodPost, Path: "/create", Security: authz.Require(authz.RoleAdmin)},
		func(_ context.Context, _ *in) (*okOut, error) { ran++; return &okOut{}, nil })

	invalid := map[string]any{"name": "x"} // fails minLength
	if rec := e.api.Post("/create", invalid); rec.Code != http.StatusUnauthorized {
		t.Errorf("no credentials + invalid body: status = %d, want 401 (auth before validation)", rec.Code)
	}
	if rec := e.api.Post("/create", bearer("client-token"), invalid); rec.Code != http.StatusForbidden {
		t.Errorf("client + invalid body: status = %d, want 403", rec.Code)
	}
	if rec := e.api.Post("/create", bearer("admin-token"), invalid); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("admin + invalid body: status = %d, want 422 (validation still applies after auth)", rec.Code)
	}
	if rec := e.api.Post("/create", bearer("admin-token"), map[string]any{"name": "valid"}); rec.Code != http.StatusOK {
		t.Errorf("admin + valid body: status = %d, want 200", rec.Code)
	}
	if ran != 1 {
		t.Errorf("handler ran %d times, want exactly 1", ran)
	}
}

func TestMiddleware_HandlerNeverRunsOnFailure(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "admin-only", Method: http.MethodDelete, Path: "/things", Security: authz.Require(authz.RoleAdmin)})
	for _, hdr := range [][]any{nil, {bearer("bad")}, {bearer("client-token")}} {
		if rec := e.api.Delete("/things", hdr...); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 401 or 403", rec.Code)
		}
	}
	if e.ran != 0 {
		t.Errorf("handler ran %d times on failing requests", e.ran)
	}
}

// ---- startup guard ----

func TestAssertDeclared_PassesOnFullyDeclaredAPI(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "a", Path: "/a", Security: authz.Public()})
	e.register(huma.Operation{OperationID: "b", Path: "/b", Security: authz.Require()})
	e.register(huma.Operation{OperationID: "c", Path: "/c", Security: authz.Require(authz.RoleAdmin)})
	e.register(huma.Operation{OperationID: "d", Path: "/d", Security: authz.ChallengeOnly()})
	e.register(huma.Operation{OperationID: "e", Path: "/e", Hidden: true, Security: authz.SelfAuthenticated(), Metadata: map[string]any{"authz": "self"}})
	mustNotPanic(t, "AssertDeclared", func() { authz.AssertDeclared(e.api) })
}

func TestAssertDeclared_PanicsOnNilSecurity(t *testing.T) {
	e := newEnv(t)
	e.register(huma.Operation{OperationID: "ok", Path: "/ok", Security: authz.Public()})
	mustPanic(t, "nil Security", func() {
		e.register(huma.Operation{OperationID: "forgot", Path: "/forgot"})
		authz.AssertDeclared(e.api)
	})
}

func TestAssertDeclared_PanicsOnUnknownScheme(t *testing.T) {
	e := newEnv(t)
	mustPanic(t, "unknown scheme", func() {
		e.register(huma.Operation{OperationID: "weird", Path: "/weird", Security: []map[string][]string{{"apiKey": {}}}})
		authz.AssertDeclared(e.api)
	})
}

func TestAssertDeclared_PanicsOnSchemeWithoutVerifier(t *testing.T) {
	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))
	authz.Install(api, map[string]authz.Verifier{"bearer": &fakeVerifier{}}) // no mfaChallenge verifier
	mustPanic(t, "scheme with no verifier", func() {
		huma.Register(api, huma.Operation{OperationID: "mfa", Method: http.MethodGet, Path: "/mfa", Security: authz.ChallengeOnly()},
			func(context.Context, *empty) (*okOut, error) { return &okOut{}, nil })
		authz.AssertDeclared(api)
	})
}

func TestAssertDeclared_PanicsOnMultipleRequirementObjects(t *testing.T) {
	e := newEnv(t)
	mustPanic(t, "two requirement objects", func() {
		e.register(huma.Operation{OperationID: "two", Path: "/two", Security: []map[string][]string{{"bearer": {}}, {"mfaChallenge": {}}}})
		authz.AssertDeclared(e.api)
	})
}

func TestAssertDeclared_PanicsOnSelfAuthenticatedDocumentedOperation(t *testing.T) {
	e := newEnv(t)
	mustPanic(t, "SelfAuthenticated on a documented operation", func() {
		e.register(huma.Operation{OperationID: "bad", Path: "/bad", Security: authz.SelfAuthenticated(), Metadata: map[string]any{"authz": "self"}})
		authz.AssertDeclared(e.api)
	})
}
