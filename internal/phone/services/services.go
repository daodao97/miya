// Package services reads authenticated phone runtime and web-service listings.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"miya/internal/chat/model"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/runtime/tracker"
	webservice "miya/internal/runtime/web"
	"miya/locales"
)

func tr(s string) string { return locales.T(s) }
func FetchPhoneWebServices(ctx context.Context, dial func(context.Context) (net.Conn, error), project string) ([]webservice.Service, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop/"+phoneproto.ProjectFileID(project)+"/"+webservice.ProjectFile, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("project web services unavailable")
	}
	var services []webservice.Service
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&services); err != nil {
		return nil, err
	}
	for _, service := range services {
		if !webservice.ValidHost(service.Host, service.RuntimeToken) {
			return nil, errors.New("invalid service address")
		}
		if _, err := webservice.ParsePort(strconv.Itoa(service.Port)); err != nil {
			return nil, err
		}
	}
	return services, nil
}

func FetchPhoneRuntime(ctx context.Context, dial func(context.Context) (net.Conn, error), project string, ref model.ThreadRef) (tracker.ConversationRuntime, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop/"+phoneproto.ProjectFileID(project)+"/.miya-runtime?ref="+url.QueryEscape(ref.URL()), nil)
	if err != nil {
		return tracker.ConversationRuntime{}, err
	}
	response, err := client.Do(req)
	if err != nil {
		return tracker.ConversationRuntime{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return tracker.ConversationRuntime{}, errors.New(tr("Conversation runtime information unavailable"))
	}
	var result tracker.ConversationRuntime
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return result, err
	}
	if result.View.Key != tracker.KeyOf(ref) {
		return tracker.ConversationRuntime{}, errors.New(tr("Conversation runtime identity mismatch"))
	}
	for _, service := range result.Services {
		if service.Host == "" || !webservice.ValidHost(service.Host, service.RuntimeToken) || service.Port < 1 || service.Port > 65535 || service.RuntimeToken == "" {
			return tracker.ConversationRuntime{}, errors.New(tr("Invalid runtime service endpoint"))
		}
	}
	return result, nil
}
