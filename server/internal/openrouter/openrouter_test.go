package openrouter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

const modelsFixture = `{ "data": [
  { "id": "google/gemini-4-hydrogen", "context_length": 1048576, "supported_parameters": ["tools", "structured_outputs"] },
  { "id": "old/no-schema", "supported_parameters": ["response_format"] },
  { "id": "old/no-parameters" },
  { "id": "anthropic/claude-sonnet-5.5", "supported_parameters": ["structured_outputs"] }
] }`

func TestOnlyModelsWithStructuredOutputsAreListed(t *testing.T) {
	ids, err := ParseModels([]byte(modelsFixture))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"google/gemini-4-hydrogen", "anthropic/claude-sonnet-5.5"}; !slices.Equal(ids, want) {
		t.Fatalf("got %v", ids)
	}
}

func fakeOpenRouter(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func TestModelsChecksTheKeyFirst(t *testing.T) {
	var paths []string
	url := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer sk-or-good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/models/user" {
			io.WriteString(w, modelsFixture)
			return
		}
		io.WriteString(w, `{"data":{}}`)
	})
	ids, err := New(url, "sk-or-good").Models(context.Background())
	if err != nil || len(ids) != 2 {
		t.Fatalf("got %v, %v", ids, err)
	}
	if !slices.Equal(paths, []string{"/key", "/models/user"}) {
		t.Fatalf("paths %v", paths)
	}
}

func TestWrongKeyIsInvalid(t *testing.T) {
	url := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/key" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"User not found.","code":401}}`)
	})
	_, err := New(url, "sk-or-v1-invalid").Models(context.Background())
	if !errors.Is(err, ErrInvalidKey) || err.Error() != "Invalid API key" {
		t.Fatalf("got %v", err)
	}
}

func TestErrorMessageIsReported(t *testing.T) {
	url := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, `{"error":{"message":"Insufficient credits","code":402}}`)
	})
	_, err := New(url, "k").ChatCompletion(context.Background(), []byte(`{}`))
	if err == nil || err.Error() != "OpenRouter: Insufficient credits" {
		t.Fatalf("got %v", err)
	}
}

func TestStatusIsReportedWithoutMessage(t *testing.T) {
	url := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := New(url, "k").ChatCompletion(context.Background(), []byte(`{}`))
	if err == nil || err.Error() != "OpenRouter returned HTTP 502" {
		t.Fatalf("got %v", err)
	}
}

func TestCancelStopsCompletion(t *testing.T) {
	started := make(chan struct{})
	url := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type %q", r.Header.Get("Content-Type"))
		}
		io.ReadAll(r.Body)
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	begin := time.Now()
	_, err := New(url, "k").ChatCompletion(ctx, []byte(`{}`))
	if !errors.Is(err, context.Canceled) || time.Since(begin) > 5*time.Second {
		t.Fatalf("got %v after %v", err, time.Since(begin))
	}
}

func TestResponseSizeIsLimited(t *testing.T) {
	url := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte(" "), maxBodyBytes+1))
	})
	_, err := New(url, "k").ChatCompletion(context.Background(), []byte(`{}`))
	if err == nil || err.Error() != "The OpenRouter response is larger than 10 MB" {
		t.Fatalf("got %v", err)
	}
}
