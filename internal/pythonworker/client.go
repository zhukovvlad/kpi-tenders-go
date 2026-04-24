package pythonworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is an HTTP client for the internal Python worker service.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new Client for the given base URL.
func New(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ProcessRequest is the payload sent to POST /process on the Python worker.
type ProcessRequest struct {
	TaskID      string `json:"task_id"`
	DocumentID  string `json:"document_id"`
	ModuleName  string `json:"module_name"`
	StoragePath string `json:"storage_path"`
}

// Process calls POST /process on the Python worker.
// No auth header is set — the Python service is internal and unreachable
// from outside the private network.
func (c *Client) Process(ctx context.Context, req ProcessRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("pythonworker: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/process", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pythonworker: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("pythonworker: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pythonworker: process returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}
