package chrome

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/fetch"
	"github.com/mafredri/cdp/protocol/network"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/protocol/runtime"
	"github.com/mafredri/cdp/protocol/target"
	"github.com/mafredri/cdp/rpcc"
	"github.com/mafredri/cdp/session"
	"net/http"
	"strings"
	"sync"
)

func init() {
	caddy.RegisterModule(Chrome{})
	httpcaddyfile.RegisterHandlerDirective("chrome", parseCaddyfile)
}

type Chrome struct {
	// Chrome Devtools Protocol URL.
	URL string
	// MIME types for which to render using Chrome. Defaults to text/html.
	MIMETypes []string
}

var defaultMIMETypes = []string{"text/html"}

var bufPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

func (Chrome) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID: "http.handlers.chrome",
		New: func() caddy.Module {
			return &Chrome{}
		},
	}
}

func (c *Chrome) Provision(caddy.Context) error {
	if c.MIMETypes == nil {
		c.MIMETypes = defaultMIMETypes
	}
	return nil
}

func (c *Chrome) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)

	recorder := caddyhttp.NewResponseRecorder(w, buf, func(status int, header http.Header) bool {
		contentType := header.Get("Content-Type")
		for _, mimeType := range c.MIMETypes {
			if strings.Contains(contentType, mimeType) {
				return true
			}
		}
		return false
	})

	err := next.ServeHTTP(recorder, r)
	if err != nil {
		return err
	}
	if !recorder.Buffered() {
		return nil
	}

	ctx := r.Context()

	dt := devtool.New(c.URL)
	version, err := dt.Version(ctx)
	if err != nil {
		return err
	}

	rpccConn, err := rpcc.DialContext(ctx, version.WebSocketDebuggerURL)
	if err != nil {
		return err
	}
	defer rpccConn.Close()

	browserClient := cdp.NewClient(rpccConn)

	sessionManager, err := session.NewManager(browserClient)
	if err != nil {
		return err
	}
	defer sessionManager.Close()

	browserContextReply, err := browserClient.Target.CreateBrowserContext(ctx, target.NewCreateBrowserContextArgs())
	if err != nil {
		return err
	}

	targetReply, err := browserClient.Target.CreateTarget(
		ctx,
		target.NewCreateTargetArgs("about:blank").SetBrowserContextID(browserContextReply.BrowserContextID),
	)
	if err != nil {
		return err
	}

	targetConn, err := sessionManager.Dial(ctx, targetReply.TargetID)
	if err != nil {
		return err
	}
	defer targetConn.Close()

	client := cdp.NewClient(targetConn)

	err = client.Page.Enable(ctx)
	if err != nil {
		return err
	}

	loadEventFiredClient, err := client.Page.LoadEventFired(ctx)
	if err != nil {
		return err
	}

	err = client.Fetch.Enable(ctx, fetch.NewEnableArgs())
	if err != nil {
		return err
	}

	requestPausedClient, err := client.Fetch.RequestPaused(ctx)
	if err != nil {
		return err
	}

	navigateUrl := "http://" + r.Host + r.URL.RequestURI() // FIXME: https?

	go func() {
	LOOP:
		for {
			fmt.Println("LOOP")
			select {
			case <-ctx.Done():
				break LOOP
			case <-requestPausedClient.Ready():
				// TODO: handle sub requests concurrently
				paused, err := requestPausedClient.Recv()
				if err != nil {
					fmt.Println(err)
					break LOOP
				}

				fmt.Printf("PAUSED: %#v\n", paused)

				var res response

				if paused.ResourceType == network.ResourceTypeDocument && paused.Request.URL == navigateUrl {
					res = recorder
				} else {
					subRequest, err := http.NewRequestWithContext(ctx, paused.Request.Method, paused.Request.URL, nil)
					if err != nil {
						panic(err)
					}
					var headers map[string]string
					err = json.Unmarshal(paused.Request.Headers, &headers)
					if err != nil {
						panic(err)
					}
					for key, value := range headers {
						subRequest.Header.Set(key, value)
					}

					subResponse := &subResponseWriter{header: make(http.Header)}
					server := r.Context().Value(caddyhttp.ServerCtxKey).(http.Handler)
					server.ServeHTTP(subResponse, subRequest)

					res = subResponse
				}

				args := fetch.NewFulfillRequestArgs(paused.RequestID, res.Status())
				var headers []fetch.HeaderEntry
				for headerName, headerValues := range res.Header() {
					for _, headerValue := range headerValues {
						headers = append(headers, fetch.HeaderEntry{
							Name:  headerName,
							Value: headerValue,
						})
					}
				}
				args.SetResponseHeaders(headers)
				args.SetBody(base64.StdEncoding.EncodeToString(res.Buffer().Bytes()))
				err = client.Fetch.FulfillRequest(ctx, args)
				fmt.Printf("FULFILL: %#v, %#v", args, err)
				if err != nil {
					panic(err)
				}
			}
		}
		fmt.Println("END LOOP")
	}()

	navigateReply, err := client.Page.Navigate(ctx, page.NewNavigateArgs(navigateUrl))
	fmt.Printf("NAVIGATE: %#v, %#v\n", navigateReply, err)
	if err != nil {
		return err
	}
	if navigateReply.ErrorText != nil {
		return errors.New(*navigateReply.ErrorText)
	}

	<-loadEventFiredClient.Ready()

	evalReply, err := client.Runtime.Evaluate(ctx, runtime.NewEvaluateArgs("document.documentElement.outerHTML;"))
	if err != nil {
		return err
	}
	if evalReply.ExceptionDetails != nil {
		return evalReply.ExceptionDetails
	}

	var outerHTML string
	err = json.Unmarshal(evalReply.Result.Value, &outerHTML)
	if err != nil {
		return err
	}

	closeTargetReply, err := browserClient.Target.CloseTarget(ctx, target.NewCloseTargetArgs(targetReply.TargetID))
	if err != nil {
		return err
	}
	if !closeTargetReply.Success {
		return errors.New("close target failed: " + string(targetReply.TargetID))
	}

	err = browserClient.Target.DisposeBrowserContext(ctx, target.NewDisposeBrowserContextArgs(browserContextReply.BrowserContextID))
	if err != nil {
		return err
	}

	recorder.Header().Del("Content-Length")
	recorder.Header().Del("Accept-Ranges")
	recorder.Header().Del("Etag")
	recorder.Header().Del("Last-Modified")

	recorder.Buffer().Reset()
	recorder.Buffer().WriteString("<!doctype html>\n")
	recorder.Buffer().WriteString(outerHTML)

	return recorder.WriteResponse()
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	m := &Chrome{}
	err := m.UnmarshalCaddyfile(h.Dispenser)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (c *Chrome) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		c.URL = d.Val()
	}
	return nil
}

type response interface {
	Status() int
	Header() http.Header
	Buffer() *bytes.Buffer
}

type subResponseWriter struct {
	statusCode int
	header     http.Header
	buf        bytes.Buffer
}

func (w *subResponseWriter) Status() int {
	return w.statusCode
}

func (w *subResponseWriter) Header() http.Header {
	return w.header
}

func (w *subResponseWriter) Buffer() *bytes.Buffer {
	return &w.buf
}

func (w *subResponseWriter) Write(data []byte) (int, error) {
	return w.buf.Write(data)
}

func (w *subResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

var (
	_ caddyhttp.MiddlewareHandler = (*Chrome)(nil)
	_ caddy.Module                = (*Chrome)(nil)
	_ caddy.Provisioner           = (*Chrome)(nil)
	_ caddyfile.Unmarshaler       = (*Chrome)(nil)
)
