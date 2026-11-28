package controller

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"miya/internal/agent/integrations"
	"miya/internal/appdir"
	"miya/internal/async"
	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/prefs"
	"miya/internal/remote"
	svcclient "miya/internal/service/client"
	svcproto "miya/internal/service/protocol"
	"miya/internal/translate"
)

// translations are the replies translated in this process, for the desktop
// and its phones alike: a second tap, or the same reply on the phone, does
// not ask the agent again.
var (
	translationsOnce sync.Once
	translations     *translate.Cache
	// translating counts the translations an agent is making here: the
	// service does not quit for an update under them (ServiceBusy).
	translating atomic.Int64
	// flights are the translations under way, by key: a second ask for one,
	// from a phone as the desktop's is under way, waits for it rather than
	// asking the agent again; windows and phones show them translating.
	flightsMu sync.Mutex
	flights   = map[string]*flight{}
)

// translationsPath is the file the translations outlast the service in:
// windows and phones that show a reply after an update find it translated.
// Tests keep them in memory.
var translationsPath = func() string {
	if testing.Testing() {
		return ""
	}
	return filepath.Join(appdir.Dir(), "translations.json.gz")
}

// translateWith asks an agent for a translation; tests replace it.
var translateWith = integrations.Translate

type flight struct {
	done chan struct{}
	text string
	err  error
}

func translationCache() *translate.Cache {
	translationsOnce.Do(func() { translations = translate.NewCache(translationsPath()) })
	return translations
}

// translatingKeys are the keys of the translations under way, sorted.
func translatingKeys() []string {
	flightsMu.Lock()
	defer flightsMu.Unlock()
	keys := make([]string, 0, len(flights))
	for k := range flights {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applyTranslationSettings keeps how replies translate, for the windows and
// phones the state shows it to. On the main thread.
func (c *Controller) applyTranslationSettings(s translate.Settings) {
	s = s.Normalize()
	if s == c.Translation {
		return
	}
	c.Translation = s
	if c.Remote != nil {
		cl := c.Remote
		go cl.Call("translationSettings", s, nil)
	} else {
		s.Save(prefs.Save)
	}
	c.stateChanged()
}

// SetTranslationSettings keeps a window's change of how replies translate
// where the agents run. On the main thread.
func (c *Controller) SetTranslationSettings(s translate.Settings) { c.applyTranslationSettings(s) }

// stateChanged has the state sent again to windows and phones.
func (c *Controller) stateChanged() {
	if c.Revision != nil {
		c.SetRevision(c.Revision() + 1)
	}
}

// prepareTranslate captures the configuration on the main thread; the work
// it returns asks the agent off it.
func (c *Controller) prepareTranslate(ctx context.Context, r translate.Request) (async.Reply, error) {
	if strings.TrimSpace(r.Text) == "" {
		return nil, errors.New("Nothing to translate")
	}
	r.Agent, r.Lang = model.NormalizeAgentID(r.Agent), translate.TargetFor(r.Lang).Tag
	r.Project = strings.TrimSpace(r.Project)
	if r.Project == "" {
		r.Project = c.Cwd
	}
	agents, cache := integrations.TranslateAgents{Claude: c.claudeCLI()}, translationCache()
	if pi := c.piSnapshot(); pi.Binary != "" {
		agents.Pi = &pi
	}
	return async.Reply(func() (any, error) {
		if s, ok := cache.Get(r.Text, r.Lang); ok {
			return translate.Result{Text: s}, nil
		}
		key := translate.Key(r.Text, r.Lang)
		flightsMu.Lock()
		f, waiting := flights[key]
		if !waiting {
			f = &flight{done: make(chan struct{})}
			flights[key] = f
		}
		flightsMu.Unlock()
		if waiting {
			select {
			case <-f.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if f.err != nil {
				return nil, f.err
			}
			return translate.Result{Text: f.text}, nil
		}
		c.Dispatch(c.stateChanged)
		translating.Add(1)
		f.text, f.err = translateWith(ctx, agents, r)
		translating.Add(-1)
		if f.err == nil {
			cache.Put(r.Text, r.Lang, f.text)
		}
		flightsMu.Lock()
		delete(flights, key)
		flightsMu.Unlock()
		close(f.done)
		// Windows and phones that show the reply learn of it.
		c.Dispatch(c.translationsChanged)
		if f.err != nil {
			return nil, f.err
		}
		return translate.Result{Text: f.text}, nil
	}), nil
}

// translationsChanged tells windows and phones a reply was translated.
func (c *Controller) translationsChanged() {
	c.TranslationRevision++
	c.stateChanged()
}

// TranslationLookup returns the translations kept of keys where the agents
// run, by key. It may wait for the service: call it off the main thread.
func (c *Controller) TranslationLookup(keys []string) (map[string]string, error) {
	if c.Remote != nil {
		out := map[string]string{}
		err := c.Remote.Call("translationLookup", translate.Lookup{Keys: keys}, &out)
		return out, err
	}
	return translationCache().Lookup(keys), nil
}

// Translate puts r.Text into the language r.Lang where the agents run: the
// background service of a window, else here. It waits for an agent: call it
// off the main thread.
func (c *Controller) Translate(r translate.Request) (string, error) {
	if cl := c.Remote; cl != nil {
		var out translate.Result
		err := cl.Call("translate", r, &out)
		if err != nil && err.Error() == svcproto.ErrClosed.Error() {
			// The service went, as for an update: ask the one the window
			// reconnects to.
			if next := c.awaitReconnect(cl, 30*time.Second); next != nil {
				err = next.Call("translate", r, &out)
			}
		}
		if err != nil {
			return "", err
		}
		return out.Text, nil
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return c.translateHere(ctx, r)
}

// awaitReconnect waits up to limit for the window to reconnect to its
// service after old went, returning the new client, or nil.
func (c *Controller) awaitReconnect(old *svcclient.Client, limit time.Duration) *svcclient.Client {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if cl := c.Remote; cl != nil && cl != old {
			select {
			case <-cl.Done():
			default:
				return cl
			}
		}
	}
	return nil
}

// translateHere translates where the agents run, off the main thread.
func (c *Controller) translateHere(ctx context.Context, r translate.Request) (string, error) {
	type prepared struct {
		work async.Reply
		err  error
	}
	p, ok := async.OnMain(ctx, c.Dispatch, func() prepared {
		work, err := c.prepareTranslate(ctx, r)
		return prepared{work, err}
	})
	if !ok {
		return "", ctx.Err()
	}
	if p.err != nil {
		return "", p.err
	}
	res, err := p.work()
	if err != nil {
		return "", err
	}
	return res.(translate.Result).Text, nil
}

func phoneTranslateFrame(r remote.Request, text string, err error) remote.Frame {
	data, _ := json.Marshal(phoneproto.TranslateResult{RequestID: r.RequestID, Text: text})
	f := remote.Frame{Type: "translate", Data: data}
	if err != nil {
		f.Error = err.Error()
	}
	return f
}

// phoneTranslate translates a phone's reply text, off the main thread.
func (c *Controller) phoneTranslate(ctx context.Context, r remote.Request) remote.Frame {
	text, err := c.translateHere(ctx, translate.Request{Text: r.Text, Lang: r.Lang, Agent: r.Agent, Model: r.Model, Project: r.Project})
	return phoneTranslateFrame(r, text, err)
}
