// Tests for RFC 9457 problems with stable codes (T006; FR-091, FR-093, plan P-10, contracts/error-codes.md).
//
// API assumptions (contracts are silent):
//   - httpx.InstallErrors() takes no arguments and is idempotent; it must be called before the
//     operations under test are registered (tests call it first in every test).
//   - httpx.Problem(status int, code, detail string) returns an error that implements
//     huma.StatusError (GetStatus() == status), so handlers may "return nil, httpx.Problem(...)".
//   - httpx.ErrorModel embeds huma.ErrorModel and adds Code string `json:"code"`.
//   - A 500 produced from an ordinary Go error never echoes err.Error() ("no details").
package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
)

type errBody struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
	Errors []map[string]any
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errBody {
	t.Helper()
	var b errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body.String())
	}
	return b
}

type loginIn struct {
	Body struct {
		Identifier string `json:"identifier" minLength:"3"`
		Password   string `json:"password" minLength:"10"`
	}
}

type none struct{}

func newAPI(t *testing.T, h func(context.Context, *none) (*struct{}, error)) humatest.TestAPI {
	t.Helper()
	httpx.InstallErrors()
	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))
	huma.Register(api, huma.Operation{OperationID: "login", Method: http.MethodPost, Path: "/login", Security: []map[string][]string{}},
		func(context.Context, *loginIn) (*struct{}, error) { return &struct{}{}, nil })
	huma.Register(api, huma.Operation{OperationID: "thing", Method: http.MethodGet, Path: "/thing", Security: []map[string][]string{}}, h)
	return api
}

func failWith(err error) func(context.Context, *none) (*struct{}, error) {
	return func(context.Context, *none) (*struct{}, error) { return nil, err }
}

func TestErrors_ProblemCarriesCodeAndTypeURN(t *testing.T) {
	api := newAPI(t, failWith(httpx.Problem(http.StatusConflict, "auth.email_taken", "that email is already in use")))
	rec := api.Get("/thing")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	b := decodeErr(t, rec)
	if b.Code != "auth.email_taken" {
		t.Errorf("code = %q", b.Code)
	}
	if b.Type != "urn:swastik:problem:auth.email_taken" {
		t.Errorf("type = %q, want urn:swastik:problem:auth.email_taken", b.Type)
	}
	if b.Status != 409 || b.Title != "Conflict" || b.Detail != "that email is already in use" {
		t.Errorf("status/title/detail = %d/%q/%q", b.Status, b.Title, b.Detail)
	}
}

func TestErrors_ProblemIsStatusError(t *testing.T) {
	httpx.InstallErrors()
	err := httpx.Problem(http.StatusUnauthorized, "auth.invalid_credentials", "Those details didn't work.")
	var se huma.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("Problem() = %T, does not implement huma.StatusError", err)
	}
	if se.GetStatus() != http.StatusUnauthorized {
		t.Errorf("GetStatus() = %d, want 401", se.GetStatus())
	}
}

func TestErrors_SameProblemIsByteIdentical(t *testing.T) {
	// D-09 / SC-003: the generic sign-in failure must be indistinguishable byte for byte.
	api := newAPI(t, failWith(httpx.Problem(http.StatusUnauthorized, "auth.invalid_credentials", "Those details didn't work.")))
	a, b := api.Get("/thing"), api.Get("/thing")
	if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
		t.Errorf("bodies differ:\n%s\n%s", a.Body.String(), b.Body.String())
	}
}

func TestErrors_DefaultCodePerStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{"400", huma.Error400BadRequest("nope"), "bad_request"},
		{"404", huma.Error404NotFound("nope"), "not_found"},
		{"415", huma.Error415UnsupportedMediaType("nope"), "unsupported_media_type"},
		{"422", huma.Error422UnprocessableEntity("nope"), "validation_failed"},
		{"429", huma.Error429TooManyRequests("nope"), "rate_limited"},
		{"500 huma", huma.Error500InternalServerError("nope"), "internal"},
		{"500 plain error", errors.New("boom"), "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newAPI(t, failWith(tt.err)).Get("/thing")
			b := decodeErr(t, rec)
			if b.Code != tt.code {
				t.Errorf("code = %q, want %q (status %d)", b.Code, tt.code, rec.Code)
			}
			if want := "urn:swastik:problem:" + tt.code; b.Type != want {
				t.Errorf("type = %q, want %q", b.Type, want)
			}
		})
	}
}

func TestErrors_FrameworkErrorsGetCodes(t *testing.T) {
	api := newAPI(t, failWith(nil))

	t.Run("malformed JSON is 400 bad_request", func(t *testing.T) {
		rec := api.Post("/login", "Content-Type: application/json", strings.NewReader(`{"identifier":"a@b.in","password":"hunter2-secret",`))
		b := decodeErr(t, rec)
		if rec.Code != http.StatusBadRequest || b.Code != "bad_request" {
			t.Errorf("status/code = %d/%q, want 400/bad_request", rec.Code, b.Code)
		}
	})
	t.Run("schema violation is 422 validation_failed", func(t *testing.T) {
		rec := api.Post("/login", map[string]any{"identifier": "a@b.in", "password": "short"})
		b := decodeErr(t, rec)
		if rec.Code != http.StatusUnprocessableEntity || b.Code != "validation_failed" {
			t.Errorf("status/code = %d/%q, want 422/validation_failed", rec.Code, b.Code)
		}
		if len(b.Errors) == 0 {
			t.Fatal("validation problem lacks errors[]")
		}
		if loc, _ := b.Errors[0]["location"].(string); loc != "body.password" {
			t.Errorf("errors[0].location = %q, want body.password", loc)
		}
	})
	t.Run("non-JSON content type is 415 unsupported_media_type", func(t *testing.T) {
		rec := api.Post("/login", "Content-Type: text/plain", strings.NewReader(`password=hunter2-secret`))
		b := decodeErr(t, rec)
		if rec.Code != http.StatusUnsupportedMediaType || b.Code != "unsupported_media_type" {
			t.Errorf("status/code = %d/%q, want 415/unsupported_media_type", rec.Code, b.Code)
		}
	})
}

func TestErrors_ValueIsNeverEchoed(t *testing.T) {
	api := newAPI(t, failWith(nil))
	const secret = "hunter2-very-secret"
	cases := map[string]*httptest.ResponseRecorder{
		"malformed JSON containing a password": api.Post("/login", "Content-Type: application/json",
			strings.NewReader(`{"identifier":"a@b.in","password":"`+secret+`",`)),
		"validation error on the password field": api.Post("/login", map[string]any{"identifier": "a@b.in", "password": secret[:5]}),
		"validation error on a wrong type":       api.Post("/login", map[string]any{"identifier": 12345, "password": secret}),
		"unsupported content type":               api.Post("/login", "Content-Type: text/plain", strings.NewReader(secret)),
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			body := rec.Body.String()
			if strings.Contains(body, secret) || strings.Contains(body, secret[:5]) {
				t.Errorf("response echoes submitted data:\n%s", body)
			}
			var generic map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &generic); err != nil {
				t.Fatal(err)
			}
			if errs, ok := generic["errors"].([]any); ok {
				for i, e := range errs {
					if m, ok := e.(map[string]any); ok {
						if _, has := m["value"]; has {
							t.Errorf("errors[%d] has a value member: %v", i, m)
						}
					}
				}
			}
		})
	}
}

func TestErrors_NewErrorDropsDetailValue(t *testing.T) {
	httpx.InstallErrors()
	detail := &huma.ErrorDetail{Message: "value is too short", Location: "body.password", Value: "hunter2-very-secret"}
	for name, err := range map[string]huma.StatusError{
		"NewError":            huma.NewError(http.StatusUnprocessableEntity, "validation failed", detail),
		"NewErrorWithContext": huma.NewErrorWithContext(nil, http.StatusUnprocessableEntity, "validation failed", detail),
	} {
		t.Run(name, func(t *testing.T) {
			raw, mErr := json.Marshal(err)
			if mErr != nil {
				t.Fatal(mErr)
			}
			if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), `"value"`) {
				t.Errorf("marshalled error carries the value: %s", raw)
			}
			if !strings.Contains(string(raw), "body.password") || !strings.Contains(string(raw), "value is too short") {
				t.Errorf("location and message must survive: %s", raw)
			}
			if !strings.Contains(string(raw), `"code":"validation_failed"`) {
				t.Errorf("code missing from %s", raw)
			}
		})
	}
}

func TestErrors_InternalErrorDoesNotLeakCause(t *testing.T) {
	api := newAPI(t, failWith(errors.New("pq: password authentication failed for user swastik at 10.0.0.7")))
	rec := api.Get("/thing")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "10.0.0.7") || strings.Contains(body, "pq:") {
		t.Errorf("500 response leaks the underlying error: %s", body)
	}
}

func TestErrors_InstallErrorsIsIdempotent(t *testing.T) {
	httpx.InstallErrors()
	httpx.InstallErrors()
	httpx.InstallErrors()
	rec := newAPI(t, failWith(httpx.Problem(http.StatusConflict, "x.y", "d"))).Get("/thing")
	b := decodeErr(t, rec)
	if b.Code != "x.y" || b.Type != "urn:swastik:problem:x.y" {
		t.Errorf("after repeated InstallErrors: code=%q type=%q", b.Code, b.Type)
	}
	// A framework error still gets exactly one code member (no double wrapping).
	rec = newAPI(t, failWith(huma.Error404NotFound("nope"))).Get("/thing")
	if n := strings.Count(rec.Body.String(), `"code"`); n != 1 {
		t.Errorf(`body has %d "code" members, want 1: %s`, n, rec.Body.String())
	}
}

func TestErrors_SchemaNameStaysErrorModelWithCode(t *testing.T) {
	api := newAPI(t, failWith(nil))
	schemas := api.OpenAPI().Components.Schemas.Map()
	s, ok := schemas["ErrorModel"]
	if !ok {
		names := make([]string, 0, len(schemas))
		for k := range schemas {
			names = append(names, k)
		}
		t.Fatalf("components.schemas lacks ErrorModel (have %v)", names)
	}
	if _, ok := s.Properties["code"]; !ok {
		t.Errorf("ErrorModel schema has no code property: %v", s.Properties)
	}
	for _, k := range []string{"status", "title", "detail"} {
		if _, ok := s.Properties[k]; !ok {
			t.Errorf("ErrorModel schema lost the %q property", k)
		}
	}
	if s.Properties["code"] != nil && s.Properties["code"].Enum != nil {
		t.Error("code must be a plain string, not an enum (later additions would be breaking)")
	}
}
