// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package libhoneyreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/libhoneyreceiver"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/errorutil"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/libhoneyreceiver/encoder"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/libhoneyreceiver/internal/eventtime"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/libhoneyreceiver/internal/libhoneyevent"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/libhoneyreceiver/internal/parser"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/libhoneyreceiver/internal/response"
)

type libhoneyReceiver struct {
	cfg        *Config
	server     *http.Server
	nextTraces consumer.Traces
	nextLogs   consumer.Logs
	shutdownWG sync.WaitGroup
	obsreport  *receiverhelper.ObsReport
	settings   *receiver.Settings
}

func newLibhoneyReceiver(cfg *Config, set *receiver.Settings) (*libhoneyReceiver, error) {
	r := &libhoneyReceiver{
		cfg:        cfg,
		nextTraces: nil,
		settings:   set,
	}

	var err error
	r.obsreport, err = receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
		ReceiverID:             set.ID,
		Transport:              "http",
		ReceiverCreateSettings: *set,
	})
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *libhoneyReceiver) startHTTPServer(ctx context.Context, host component.Host) error {
	// If HTTP is not enabled, nothing to start.
	if r.cfg.HTTP == nil {
		return nil
	}

	httpMux := http.NewServeMux()

	r.settings.Logger.Info("r.nextTraces is not null so httpTracesReceiver was added", zap.Int("paths", len(r.cfg.HTTP.TracesURLPaths)))
	for _, path := range r.cfg.HTTP.TracesURLPaths {
		httpMux.HandleFunc(path, func(resp http.ResponseWriter, req *http.Request) {
			r.handleEvent(resp, req)
		})
		r.settings.Logger.Debug("Added path to HTTP server", zap.String("path", path))
	}

	if r.cfg.AuthAPI != "" {
		httpMux.HandleFunc("/1/auth", func(resp http.ResponseWriter, req *http.Request) {
			r.handleAuth(resp, req)
		})
	}

	var err error
	if r.server, err = r.cfg.HTTP.ToServer(ctx, host, r.settings.TelemetrySettings, httpMux); err != nil {
		return err
	}

	r.settings.Logger.Info("Starting HTTP server", zap.String("endpoint", r.cfg.HTTP.Endpoint))
	var hln net.Listener
	if hln, err = r.cfg.HTTP.ToListener(ctx); err != nil {
		return err
	}

	r.shutdownWG.Add(1)
	go func() {
		defer r.shutdownWG.Done()

		if err := r.server.Serve(hln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(err))
		}
	}()
	return nil
}

func (r *libhoneyReceiver) Start(ctx context.Context, host component.Host) error {
	if err := r.startHTTPServer(ctx, host); err != nil {
		return errors.Join(err, r.Shutdown(ctx))
	}

	return nil
}

// Shutdown is a method to turn off receiving.
func (r *libhoneyReceiver) Shutdown(ctx context.Context) error {
	var err error

	if r.server != nil {
		err = r.server.Shutdown(ctx)
	}

	r.shutdownWG.Wait()
	return err
}

func (r *libhoneyReceiver) registerTraceConsumer(tc consumer.Traces) {
	r.nextTraces = tc
}

func (r *libhoneyReceiver) registerLogConsumer(tc consumer.Logs) {
	r.nextLogs = tc
}

func (r *libhoneyReceiver) handleAuth(resp http.ResponseWriter, req *http.Request) {
	authURL := fmt.Sprintf("%s/1/auth", r.cfg.AuthAPI)
	authReq, err := http.NewRequest(http.MethodGet, authURL, http.NoBody)
	if err != nil {
		errJSON, _ := json.Marshal(`{"error": "failed to create auth request"}`)
		writeResponse(resp, "json", http.StatusBadRequest, errJSON)
		return
	}
	authReq.Header.Set("x-honeycomb-team", req.Header.Get("x-honeycomb-team"))
	var authClient http.Client
	authResp, err := authClient.Do(authReq)
	if err != nil {
		errJSON, _ := json.Marshal(fmt.Sprintf(`"error": "failed to send request to auth api endpoint", "message", %q}`, err.Error()))
		writeResponse(resp, "json", http.StatusBadRequest, errJSON)
		return
	}
	defer authResp.Body.Close()

	switch {
	case authResp.StatusCode == http.StatusUnauthorized:
		errJSON, _ := json.Marshal(`"error": "received 401 response for authInfo request from Honeycomb API - check your API key"}`)
		writeResponse(resp, "json", http.StatusBadRequest, errJSON)
		return
	case authResp.StatusCode > 299:
		errJSON, _ := json.Marshal(fmt.Sprintf(`"error": "bad response code from API", "status_code", %d}`, authResp.StatusCode))
		writeResponse(resp, "json", http.StatusBadRequest, errJSON)
		return
	}
	authRawBody, _ := io.ReadAll(authResp.Body)
	_, err = resp.Write(authRawBody)
	if err != nil {
		r.settings.Logger.Info("couldn't write http response")
	}
}

func (r *libhoneyReceiver) handleEvent(resp http.ResponseWriter, req *http.Request) {
	enc, ok := readContentType(resp, req)
	if !ok {
		return
	}

	dataset, err := parser.GetDatasetFromRequest(req.RequestURI)
	if err != nil {
		r.settings.Logger.Info("No dataset found in URL", zap.String("req.RequestURI", req.RequestURI))
	}

	for _, p := range r.cfg.HTTP.TracesURLPaths {
		dataset = strings.Replace(dataset, p, "", 1)
		r.settings.Logger.Debug("dataset parsed", zap.String("dataset.parsed", dataset))
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		errorutil.HTTPError(resp, err)
	}
	if err = req.Body.Close(); err != nil {
		errorutil.HTTPError(resp, err)
	}
	libhoneyevents := make([]libhoneyevent.LibhoneyEvent, 0)
	switch req.Header.Get("Content-Type") {
	case "application/x-msgpack", "application/msgpack":
		decoder := msgpack.NewDecoder(bytes.NewReader(body))
		decoder.UseLooseInterfaceDecoding(true)
		err = decoder.Decode(&libhoneyevents)
		if err != nil {
			r.settings.Logger.Info("messagepack decoding failed")
		}
		// Post-process msgpack events to ensure timestamps are set
		for i := range libhoneyevents {
			if libhoneyevents[i].MsgPackTimestamp == nil {
				if libhoneyevents[i].Time != "" {
					// Parse the time string and set MsgPackTimestamp
					propertime := eventtime.GetEventTime(libhoneyevents[i].Time)
					libhoneyevents[i].MsgPackTimestamp = &propertime
				} else {
					// No time field, use current time
					tnow := time.Now()
					libhoneyevents[i].MsgPackTimestamp = &tnow
					libhoneyevents[i].Time = eventtime.GetEventTimeDefaultString()
				}
			}
		}
		if len(libhoneyevents) > 0 {
			r.settings.Logger.Debug("Decoding with msgpack worked", zap.Time("timestamp.first.msgpacktimestamp", *libhoneyevents[0].MsgPackTimestamp), zap.String("timestamp.first.time", libhoneyevents[0].Time))
			r.settings.Logger.Debug("event zero", zap.String("event.data", libhoneyevents[0].DebugString()))
		}
	case encoder.JSONContentType:
		err = json.Unmarshal(body, &libhoneyevents)
		if err != nil {
			errorutil.HTTPError(resp, err)
		}
		if len(libhoneyevents) > 0 {
			r.settings.Logger.Debug("Decoding with json worked", zap.Time("timestamp.first.msgpacktimestamp", *libhoneyevents[0].MsgPackTimestamp), zap.String("timestamp.first.time", libhoneyevents[0].Time))
		}
	default:
		r.settings.Logger.Info("unsupported content type", zap.String("content-type", req.Header.Get("Content-Type")))
	}

	otlpLogs, otlpTraces := parser.ToPdata(dataset, libhoneyevents, r.cfg.FieldMapConfig, *r.settings.Logger)

	// Use the request context which already contains client metadata when IncludeMetadata is enabled
	ctx := req.Context()

	numLogs := otlpLogs.LogRecordCount()
	if numLogs > 0 {
		ctx = r.obsreport.StartLogsOp(ctx)
		err = r.nextLogs.ConsumeLogs(ctx, otlpLogs)
		r.obsreport.EndLogsOp(ctx, "protobuf", numLogs, err)
	}

	numTraces := otlpTraces.SpanCount()
	if numTraces > 0 {
		ctx = r.obsreport.StartTracesOp(ctx)
		err = r.nextTraces.ConsumeTraces(ctx, otlpTraces)
		r.obsreport.EndTracesOp(ctx, "protobuf", numTraces, err)
	}

	if err != nil {
		errorutil.HTTPError(resp, err)
		return
	}

	// return clean response if no errors above
	noErrors := response.MakeResponse([]int{})

	var responseBody []byte
	var contentType string

	switch enc.ContentType() {
	case encoder.MsgpackContentType:
		// For msgpack requests, return msgpack response
		responseBody, err = msgpack.Marshal(noErrors)
		contentType = encoder.MsgpackContentType
	default:
		// For JSON requests, return JSON response
		responseBody, err = json.Marshal(noErrors)
		contentType = encoder.JSONContentType
	}

	if err != nil {
		errorutil.HTTPError(resp, err)
		return
	}
	writeResponse(resp, contentType, http.StatusOK, responseBody)
}

func readContentType(resp http.ResponseWriter, req *http.Request) (encoder.Encoder, bool) {
	if req.Method != http.MethodPost {
		handleUnmatchedMethod(resp)
		return nil, false
	}

	switch getMimeTypeFromContentType(req.Header.Get("Content-Type")) {
	case encoder.JSONContentType:
		return encoder.JsEncoder, true
	case "application/x-msgpack", "application/msgpack":
		return encoder.MpEncoder, true
	default:
		handleUnmatchedContentType(resp)
		return nil, false
	}
}

func writeResponse(w http.ResponseWriter, contentType string, statusCode int, msg []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(statusCode)
	_, _ = w.Write(msg)
}

func getMimeTypeFromContentType(contentType string) string {
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return mediatype
}

func handleUnmatchedMethod(resp http.ResponseWriter) {
	status := http.StatusMethodNotAllowed
	writeResponse(resp, "text/plain", status, []byte(fmt.Sprintf("%v method not allowed, supported: [POST]", status)))
}

func handleUnmatchedContentType(resp http.ResponseWriter) {
	status := http.StatusUnsupportedMediaType
	writeResponse(resp, "text/plain", status, []byte(fmt.Sprintf("%v unsupported media type, supported: [%s, %s]", status, encoder.JSONContentType, encoder.PbContentType)))
}
