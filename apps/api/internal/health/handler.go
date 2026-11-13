package health

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type liveOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
	}
}

type readyOutput struct {
	Body Status
}

// Register adds the health operations to the API.
func Register(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "health-live",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness probe",
		Tags:        []string{"Health"},
	}, func(_ context.Context, _ *struct{}) (*liveOutput, error) {
		out := &liveOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "health-ready",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Readiness probe (checks the database)",
		Tags:        []string{"Health"},
	}, func(ctx context.Context, _ *struct{}) (*readyOutput, error) {
		st := svc.Ready(ctx)
		if st.Status != "ok" {
			return nil, huma.Error503ServiceUnavailable("database unreachable")
		}
		return &readyOutput{Body: st}, nil
	})
}
