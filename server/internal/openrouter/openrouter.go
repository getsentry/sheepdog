package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

var ErrInvalidKey = &Error{Message: "Invalid API key"}

type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

func New(baseURL, key string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		key:     key,
		// A review of a large diff can take minutes.
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

// The models endpoint answers with a full list even for a wrong key, so the key is checked first.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if _, err := c.send(ctx, http.MethodGet, "/key", nil); err != nil {
		return nil, err
	}
	data, err := c.send(ctx, http.MethodGet, "/models/user", nil)
	if err != nil {
		return nil, err
	}
	return ParseModels(data)
}

func (c *Client) ChatCompletion(ctx context.Context, body []byte) ([]byte, error) {
	return c.send(ctx, http.MethodPost, "/chat/completions", body)
}

func (c *Client) send(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidKey
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" {
			return nil, &Error{Message: "OpenRouter: " + failure.Error.Message}
		}
		return nil, &Error{Message: fmt.Sprintf("OpenRouter returned HTTP %d", resp.StatusCode)}
	}
	return data, nil
}

// The review needs a JSON schema response, so models without structured outputs are left out.
func ParseModels(data []byte) ([]string, error) {
	var response struct {
		Data []struct {
			ID                  string   `json:"id"`
			SupportedParameters []string `json:"supported_parameters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, model := range response.Data {
		if slices.Contains(model.SupportedParameters, "structured_outputs") {
			ids = append(ids, model.ID)
		}
	}
	return ids, nil
}
