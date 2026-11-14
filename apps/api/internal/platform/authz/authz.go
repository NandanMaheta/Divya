// Package authz is the fail-closed authorisation layer every HTTP operation goes through. Each
// operation declares exactly one Security requirement; one global Huma middleware enforces it
// before body parsing and validation. The package depends only on the Verifier interface, which
// internal/auth implements.
package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
)

// Role is a user's role within the organisation.
type Role string

// The two roles of the platform.
const (
	RoleAdmin  Role = "admin"
	RoleClient Role = "client"
)

// Security scheme names registered by Install.
const (
	schemeBearer       = "bearer"
	schemeMFAChallenge = "mfaChallenge"
)

// metadataKey and metadataSelf mark a Hidden operation that authenticates itself.
const (
	metadataKey  = "authz"
	metadataSelf = "self"
)

// Principal is the authenticated caller. For an mfaChallenge principal SessionID is uuid.Nil.
type Principal struct {
	UserID, OrgID, SessionID uuid.UUID
	Role                     Role
}

// Verifier turns a bearer token into a Principal. Any error means 401 auth.unauthenticated.
type Verifier interface {
	Verify(ctx context.Context, token string) (Principal, error)
}

type principalKey struct{}

// PrincipalFrom returns the authenticated principal, if the operation required one.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// MustPrincipal is for handlers of operations declared with Require; it panics otherwise.
func MustPrincipal(ctx context.Context) Principal {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		panic("authz: MustPrincipal called outside an operation declared with Require")
	}
	return p
}

// Public declares an operation anyone may call. It must also be on the contract-test allowlist.
func Public() []map[string][]string { return []map[string][]string{} }

// Require declares a signed-in caller; with roles, only those roles. No roles means any role.
func Require(roles ...Role) []map[string][]string {
	scopes := make([]string, 0, len(roles))
	for _, r := range roles {
		scopes = append(scopes, string(r))
	}
	return []map[string][]string{{schemeBearer: scopes}}
}

// ChallengeOnly declares an MFA-challenge operation (auth MFA operations only).
func ChallengeOnly() []map[string][]string {
	return []map[string][]string{{schemeMFAChallenge: {}}}
}

// SelfAuthenticated is for Hidden operations that authenticate themselves (for example the
// scheduler tick). Callers also set Metadata["authz"]="self". A documented operation using it is
// rejected by AssertDeclared and answered 500 at runtime.
func SelfAuthenticated() []map[string][]string { return []map[string][]string{} }

var knownSchemes = []string{schemeBearer, schemeMFAChallenge}

// verifiers is kept per API so Install can be called for several APIs (tests).
type installed struct {
	verifiers map[string]Verifier
}

var (
	registryMu sync.Mutex
	registry   = map[huma.API]*installed{}
)

// Install registers the security schemes and the global middleware. Call it before any
// huma.Register, because Huma captures middlewares at registration time.
func Install(api huma.API, verifiers map[string]Verifier) {
	oapi := api.OpenAPI()
	if oapi.Components == nil {
		oapi.Components = &huma.Components{}
	}
	if oapi.Components.SecuritySchemes == nil {
		oapi.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	oapi.Components.SecuritySchemes[schemeBearer] = &huma.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"}
	oapi.Components.SecuritySchemes[schemeMFAChallenge] = &huma.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "opaque"}

	in := &installed{verifiers: verifiers}
	registryMu.Lock()
	registry[api] = in
	registryMu.Unlock()
	api.UseMiddleware(middleware(in))
}

func middleware(in *installed) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if op == nil || op.Security == nil {
			writeProblem(ctx, http.StatusInternalServerError, "platform.security_undeclared",
				"The operation declares no security requirement.")
			return
		}
		if len(op.Security) == 0 {
			if op.Metadata[metadataKey] == metadataSelf && !op.Hidden {
				writeProblem(ctx, http.StatusInternalServerError, "platform.security_undeclared",
					"Self-authenticated operations must be hidden.")
				return
			}
			next(ctx)
			return
		}
		if len(op.Security) != 1 || len(op.Security[0]) != 1 {
			writeProblem(ctx, http.StatusInternalServerError, "platform.security_undeclared",
				"The operation declares an unsupported security requirement.")
			return
		}
		var scheme string
		var roles []string
		for s, r := range op.Security[0] {
			scheme, roles = s, r
		}
		verifier, ok := in.verifiers[scheme]
		if !ok || verifier == nil {
			writeProblem(ctx, http.StatusInternalServerError, "platform.security_undeclared",
				"The operation names a security scheme with no verifier.")
			return
		}
		token, ok := bearerToken(ctx.Header("Authorization"))
		if !ok {
			unauthenticated(ctx)
			return
		}
		p, err := verifier.Verify(ctx.Context(), token)
		if err != nil {
			unauthenticated(ctx)
			return
		}
		if len(roles) > 0 && !slices.Contains(roles, string(p.Role)) {
			writeProblem(ctx, http.StatusForbidden, "auth.forbidden", "Your role may not call this operation.")
			return
		}
		next(huma.WithValue(ctx, principalKey{}, p))
	}
}

// bearerToken extracts the token from "Bearer <token>" (scheme case-insensitive).
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func unauthenticated(ctx huma.Context) {
	ctx.SetHeader("WWW-Authenticate", "Bearer")
	writeProblem(ctx, http.StatusUnauthorized, "auth.unauthenticated", "Authentication is required.")
}

// writeProblem writes an RFC 9457 problem with its own code, so authz works without
// httpx.InstallErrors.
func writeProblem(ctx huma.Context, status int, code, detail string) {
	body, err := json.Marshal(httpx.Problem(status, code, detail))
	if err != nil {
		status, body = http.StatusInternalServerError, []byte(`{"status":500,"code":"internal"}`)
	}
	ctx.SetHeader("Content-Type", "application/problem+json")
	ctx.SetStatus(status)
	_, _ = ctx.BodyWriter().Write(body)
}

// AssertDeclared panics if any documented operation has nil Security, names an unknown scheme or
// one without a verifier, has more than one requirement, or is SelfAuthenticated without being
// Hidden. Call it after all huma.Register calls.
func AssertDeclared(api huma.API) {
	registryMu.Lock()
	in := registry[api]
	registryMu.Unlock()
	if in == nil {
		panic("authz: AssertDeclared called before Install")
	}
	paths := api.OpenAPI().Paths
	for path, item := range paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPut: item.Put, http.MethodPost: item.Post,
			http.MethodDelete: item.Delete, http.MethodOptions: item.Options, http.MethodHead: item.Head,
			http.MethodPatch: item.Patch, http.MethodTrace: item.Trace,
		} {
			if op == nil {
				continue
			}
			where := fmt.Sprintf("%s %s (%s)", method, path, op.OperationID)
			if op.Security == nil {
				panic("authz: operation declares no Security: " + where)
			}
			if op.Metadata[metadataKey] == metadataSelf {
				panic("authz: SelfAuthenticated is only for Hidden operations: " + where)
			}
			if len(op.Security) > 1 {
				panic("authz: more than one Security requirement: " + where)
			}
			for _, req := range op.Security {
				if len(req) != 1 {
					panic("authz: a Security requirement must name exactly one scheme: " + where)
				}
				for scheme := range req {
					if !slices.Contains(knownSchemes, scheme) {
						panic(fmt.Sprintf("authz: unknown security scheme %q: %s", scheme, where))
					}
					if v := in.verifiers[scheme]; v == nil {
						panic(fmt.Sprintf("authz: no verifier for security scheme %q: %s", scheme, where))
					}
				}
			}
		}
	}
}
