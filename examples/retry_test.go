package examples

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gojek/heimdall/v7"
	"github.com/gojek/heimdall/v7/httpclient"
)

type ctxKey string

const (
	reqTime        ctxKey = "request_time_start"
	RFC3339ZNoTNoZ        = "2006-01-02 15:04:05"
)

type retryRequestLogger struct {
	out          io.Writer
	errOut       io.Writer
	backoff      heimdall.Backoff
	currentRetry int
}

func NewRetryRequestLogger(out io.Writer, errOut io.Writer) *retryRequestLogger {
	if out == nil {
		out = os.Stdout
	}

	if errOut == nil {
		errOut = os.Stderr
	}

	return &retryRequestLogger{
		out:     out,
		errOut:  errOut,
		backoff: heimdall.NewConstantBackoff(100*time.Millisecond, 1000*time.Millisecond),
	}
}

func (rl *retryRequestLogger) OnRequestStart(req *http.Request) {
	ctx := context.WithValue(req.Context(), reqTime, time.Now())
	*req = *(req.WithContext(ctx))
}

func (rl *retryRequestLogger) OnRequestEnd(req *http.Request, res *http.Response) {}

func (rl *retryRequestLogger) Retrierfunc(retry int) time.Duration {
	rl.currentRetry = retry
	return rl.backoff.Next(retry)
}

func (rl *retryRequestLogger) OnError(req *http.Request, err error) {
	reqDurationMs := getRequestDuration(req.Context()) / time.Millisecond
	fmt.Fprintf(rl.errOut, "%s retry(%d) ERROR: %v [%dms]\n", time.Now().Format(RFC3339ZNoTNoZ), rl.currentRetry+1, err, reqDurationMs)
}

func getRequestDuration(ctx context.Context) time.Duration {
	now := time.Now()
	start := ctx.Value(reqTime)
	if start == nil {
		return 0
	}
	startTime, ok := start.(time.Time)
	if !ok {
		return 0
	}
	return now.Sub(startTime)
}

/*
2022-04-02 13:28:03 retry(1) ERROR: Get "http://127.0.0.1:43817": context deadline exceeded (Client.Timeout exceeded while awaiting headers) [3002ms]
2022-04-02 13:28:06 retry(1) ERROR: Get "http://127.0.0.1:43817": context deadline exceeded (Client.Timeout exceeded while awaiting headers) [3000ms]
2022-04-02 13:28:10 retry(2) ERROR: Get "http://127.0.0.1:43817": context deadline exceeded (Client.Timeout exceeded while awaiting headers) [3002ms]
2022-04-02 13:28:14 retry(3) ERROR: Get "http://127.0.0.1:43817": context deadline exceeded (Client.Timeout exceeded while awaiting headers) [3002ms]
*/

func TestHTTPTimeoutRetry(t *testing.T) {
	plugin := NewRetryRequestLogger(nil, nil)
	cli := httpclient.NewClient(
		httpclient.WithHTTPTimeout(3*time.Second),
		httpclient.WithRetrier(heimdall.NewRetrierFunc(plugin.Retrierfunc)),
		httpclient.WithRetryCount(3),
	)

	cli.AddPlugin(plugin)

	dummyHandler := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(4 * time.Second)
		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write([]byte(`{"response": "gateway timeout exceeded"}`))
	}

	server := httptest.NewServer(http.HandlerFunc(dummyHandler))
	defer server.Close()

	_, err := cli.Get(server.URL, http.Header{})
	if err != nil {
		fmt.Println(err)
	}
}
