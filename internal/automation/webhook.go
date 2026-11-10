package automation

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// WebhookPort is the loopback port the wake endpoint prefers; another is
// taken when it is busy.
const WebhookPort = 47321

// webhookBodyLimit bounds what a wake request brings.
const webhookBodyLimit = 1 << 20

// webhook is the local HTTP wake endpoint. It listens on the loopback
// address only while an enabled automation has an ingress trigger, and
// takes POST /wake/<automation>/<trigger> with the trigger's token as a
// bearer token. It is not reachable from other computers.
type webhook struct {
	srv *http.Server
	url string
}

// WebhookURL is where the wake endpoint listens, "" when it does not.
func (rn *Runner) WebhookURL() string {
	if rn.hook == nil {
		return ""
	}
	return rn.hook.url
}

// TriggerURL is where an ingress trigger is woken over HTTP.
func (rn *Runner) TriggerURL(a Automation, t Trigger) string {
	if base := rn.WebhookURL(); base != "" && t.Kind == TriggerIngress {
		return base + "/wake/" + a.ID + "/" + t.ID
	}
	return ""
}

// syncWebhook starts the wake endpoint when an ingress trigger needs it and
// stops it when none does. It only runs in a runner that schedules.
func (rn *Runner) syncWebhook() {
	if !rn.listen {
		return
	}
	need := rn.Store.HasIngress()
	switch {
	case need && rn.hook == nil:
		ln, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(WebhookPort))
		if err != nil {
			ln, err = net.Listen("tcp4", "127.0.0.1:0")
		}
		if err != nil {
			log.Printf("miya: wake endpoint: %v", err)
			return
		}
		h := &webhook{url: "http://" + ln.Addr().String()}
		h.srv = &http.Server{Handler: http.HandlerFunc(rn.serveWake), ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
		rn.hook = h
		go func() {
			if err := h.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("miya: wake endpoint: %v", err)
			}
		}()
	case !need && rn.hook != nil:
		rn.stopWebhook()
	}
}

func (rn *Runner) stopWebhook() {
	if rn.hook != nil {
		_ = rn.hook.srv.Close()
		rn.hook = nil
	}
}

type wakeReply struct {
	Outcome string `json:"outcome,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Error   string `json:"error,omitempty"`
}

// serveWake answers a wake request, off the host's thread: the wake itself
// runs on it.
func (rn *Runner) serveWake(w http.ResponseWriter, r *http.Request) {
	reply := func(code int, body wakeReply) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "wake" {
		reply(http.StatusNotFound, wakeReply{Error: "Not found"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		reply(http.StatusMethodNotAllowed, wakeReply{Error: "Use POST"})
		return
	}
	// A page in a browser cannot send this header across origins without a
	// preflight, which this endpoint never answers.
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		reply(http.StatusUnauthorized, wakeReply{Error: "Missing bearer token"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, webhookBodyLimit))
	if err != nil {
		reply(http.StatusBadRequest, wakeReply{Error: err.Error()})
		return
	}
	dedupe := r.Header.Get("X-Miya-Dedupe")
	if dedupe == "" {
		dedupe = r.Header.Get("X-GitHub-Delivery")
	}
	type result struct {
		wake Wake
		err  error
	}
	done := make(chan result, 1)
	rn.host.Dispatch(func() {
		wk, err := rn.Ingress(parts[1], parts[2], token, true, string(body), dedupe)
		done <- result{wk, err}
	})
	var res result
	select {
	case res = <-done:
	case <-time.After(20 * time.Second):
		reply(http.StatusGatewayTimeout, wakeReply{Error: "Miya did not answer in time"})
		return
	}
	switch {
	case IsInvalidToken(res.err):
		reply(http.StatusUnauthorized, wakeReply{Error: res.err.Error()})
	case res.err != nil && res.wake.Outcome == "":
		reply(http.StatusNotFound, wakeReply{Error: res.err.Error()})
	case res.err != nil:
		reply(http.StatusConflict, wakeReply{Outcome: res.wake.Outcome, Detail: res.wake.Detail, Error: res.err.Error()})
	default:
		reply(http.StatusAccepted, wakeReply{Outcome: res.wake.Outcome, Detail: res.wake.Detail})
	}
}
