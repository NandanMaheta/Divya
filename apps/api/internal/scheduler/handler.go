package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/retry"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/tickauth"
)

// tickWriteDeadline lets the response outlive the server's global 30 s write timeout: the tick
// may use its whole 45 s budget (data-model section 7).
const tickWriteDeadline = 90 * time.Second

type tickInput struct {
	// Optional on purpose: a missing header is answered with 401, not 422.
	Authorization string `header:"Authorization" doc:"Bearer <Google OIDC ID token> or, in local and CI runs, Bearer <TICK_DEV_TOKEN>"`
}

type tickOutput struct {
	Body TickSummary
}

// RegisterTick adds the hidden POST /internal/tick operation (FR-001, FR-004: it is not part of
// the OpenAPI document or the generated client). The caller is authenticated before any work
// starts; 401 and 403 bodies are generic and the credential is never logged.
func RegisterTick(api huma.API, t Ticker, auth tickauth.Authenticator) {
	huma.Register(api, huma.Operation{
		OperationID: "scheduler-tick",
		Method:      http.MethodPost,
		Path:        "/internal/tick",
		Summary:     "Run due scheduled jobs, then relay available outbox messages",
		Tags:        []string{"Internal"},
		Hidden:      true,
		Middlewares: huma.Middlewares{extendWriteDeadline(auth)},
	}, func(ctx context.Context, in *tickInput) (*tickOutput, error) {
		// The middleware authenticated the caller once, before extending the write deadline.
		res, ok := ctx.Value(authResultKey{}).(authResult)
		if !ok {
			return nil, rejection(ctx, errors.New("authentication did not run"))
		}
		if res.err != nil {
			return nil, rejection(ctx, res.err)
		}
		sum, err := t.Tick(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "scheduler: tick could not run", "error", retry.ErrorText(err))
			return nil, huma.Error503ServiceUnavailable("the tick could not run")
		}
		return &tickOutput{Body: sum}, nil
	})
}

type (
	authResultKey struct{}
	authResult    struct{ err error }
)

// extendWriteDeadline moves the write deadline for this route only, and only for an authenticated
// caller (authenticating once and handing the result to the handler): an unauthenticated request keeps the server's short deadline (the handler answers it
// 401 or 403). Writers without deadline support (test recorders) are fine: the tick still runs.
func extendWriteDeadline(auth tickauth.Authenticator) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		err := auth.Authenticate(ctx.Context(), ctx.Header("Authorization"))
		if err == nil {
			if derr := httpx.ExtendWriteDeadline(ctx, tickWriteDeadline); derr != nil {
				slog.Debug("scheduler: write deadline not extended", "error", derr)
			}
		}
		next(huma.WithValue(ctx, authResultKey{}, authResult{err: err}))
	}
}

// rejection maps an authentication failure to 401 (with WWW-Authenticate) or 403. Only the
// reason class is logged, never the credential or the caller identity.
func rejection(ctx context.Context, err error) error {
	if errors.Is(err, tickauth.ErrForbidden) {
		slog.WarnContext(ctx, "scheduler: tick rejected", "reason", "forbidden")
		return huma.Error403Forbidden("forbidden") //nolint:wrapcheck // an HTTP problem answer, not a failure to wrap
	}
	slog.WarnContext(ctx, "scheduler: tick rejected", "reason", "unauthenticated")
	return huma.ErrorWithHeaders(huma.Error401Unauthorized("unauthorized"), http.Header{"Www-Authenticate": []string{"Bearer"}}) //nolint:wrapcheck // an HTTP problem answer, not a failure to wrap
}
