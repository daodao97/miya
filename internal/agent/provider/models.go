package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FetchModels asks the provider for its models, through each of its
// endpoints in turn until one answers: GET /v1/models under an Anthropic
// base, /models under an OpenAI one (whose base ends in /v1). Both answer
// {"data":[{"id":…}]}. An answer also proves that URL and the key work
// together; the first failure is reported when none answers.
func FetchModels(ctx context.Context, p Provider) ([]string, error) {
	if p.APIKey == "" || len(p.Endpoints) == 0 {
		return nil, errors.New("a base URL and an API key are required")
	}
	var first error
	for _, e := range p.Endpoints {
		models, err := fetchModels(ctx, e, p.APIKey)
		if err == nil {
			return models, nil
		}
		if first == nil {
			first = err
		}
	}
	return nil, first
}

func fetchModels(ctx context.Context, e Endpoint, key string) ([]string, error) {
	url := e.BaseURL + "/models"
	if e.API == Anthropic {
		url = strings.TrimSuffix(e.BaseURL, "/v1") + "/v1/models"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// Anthropic's own API takes x-api-key; the compatible endpoints Claude
	// Code is pointed at with ANTHROPIC_AUTH_TOKEN take the bearer token.
	req.Header.Set("Authorization", "Bearer "+key)
	if e.API == Anthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return nil, fmt.Errorf("%s: HTTP %d %s", url, res.StatusCode, msg)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%s: unexpected answer: %w", url, err)
	}
	out := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}
