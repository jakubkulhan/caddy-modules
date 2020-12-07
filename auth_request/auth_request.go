package auth_request

import (
	"fmt"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"net/http"
	"net/url"
)

func init() {
	caddy.RegisterModule(AuthRequest{})
	httpcaddyfile.RegisterHandlerDirective("auth_request", parseCaddyfile)
}

type AuthRequest struct {
	URI       string `json:"uri"`
	parsedURI *url.URL
	logger    *zap.Logger
}

func (AuthRequest) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID: "http.handlers.auth_request",
		New: func() caddy.Module {
			return &AuthRequest{}
		},
	}
}

func (ar *AuthRequest) Provision(ctx caddy.Context) (err error) {
	ar.parsedURI, err = url.Parse(ar.URI)
	if err != nil {
		return err
	}
	ar.logger = ctx.Logger(ar)
	return
}

func (ar *AuthRequest) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	subURI := r.URL.ResolveReference(ar.parsedURI).String()

	subRequest, err := http.NewRequestWithContext(r.Context(), "GET", subURI, nil)
	if err != nil {
		return err
	}
	subRequest.Header = r.Header.Clone()
	subRequest.Header.Del("Content-Type")
	subRequest.Header.Del("Content-Length")

	subResponse := &subResponseWriter{header: make(http.Header)}

	server := r.Context().Value(caddyhttp.ServerCtxKey).(http.Handler)
	server.ServeHTTP(subResponse, subRequest)

	ar.logger.Debug("auth request responded",
		zap.String("method", subRequest.Method),
		zap.String("uri", subURI),
		zap.Int("status", subResponse.statusCode))

	if subResponse.statusCode == http.StatusUnauthorized {
		return caddyhttp.Error(http.StatusUnauthorized, fmt.Errorf("unauthorized"))
	} else if subResponse.statusCode == http.StatusForbidden {
		return caddyhttp.Error(http.StatusForbidden, fmt.Errorf("forbidden"))
	} else if subResponse.statusCode < 200 || subResponse.statusCode >= 300 {
		return fmt.Errorf("sub-request returned unexpected error code [%d]", subResponse.statusCode)
	}

	return next.ServeHTTP(w, r)
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	m := &AuthRequest{}
	err := m.UnmarshalCaddyfile(h.Dispenser)
	if err != nil {
		return nil, err
	}
	return m, err
}

func (ar *AuthRequest) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "uri":
				if !d.Next() {
					return d.ArgErr()
				}
				ar.URI = d.Val()
			default:
				return d.ArgErr()
			}
		}
	}
	return nil
}

type subResponseWriter struct {
	statusCode int
	header     http.Header
}

func (w *subResponseWriter) Header() http.Header {
	return w.header
}

func (w *subResponseWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func (w *subResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

var (
	_ caddyhttp.MiddlewareHandler = (*AuthRequest)(nil)
	_ caddyfile.Unmarshaler       = (*AuthRequest)(nil)
	_ http.ResponseWriter         = (*subResponseWriter)(nil)
)
