package ambatukam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func GetJSON[T any](c *Client, ctx context.Context, url string) (T, error) {
	var zero T
	resp, err := c.Get(ctx, url)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return zero, &RequestError{
			Method:   http.MethodGet,
			URL:      url,
			Status:   resp.StatusCode,
			Attempts: 1,
			Err:      fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return zero, fmt.Errorf("ambatukam: decode JSON: %w", err)
	}
	return v, nil
}

func PostJSON[T any](c *Client, ctx context.Context, url string, body any) (T, error) {
	var zero T

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return zero, fmt.Errorf("ambatukam: marshal request body: %w", err)
		}
	}

	resp, err := c.Post(ctx, url, "application/json", io.NopCloser(bytes.NewReader(bodyBytes)))
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return zero, &RequestError{
			Method:   http.MethodPost,
			URL:      url,
			Status:   resp.StatusCode,
			Attempts: 1,
			Err:      fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return zero, fmt.Errorf("ambatukam: decode JSON: %w", err)
	}
	return v, nil
}
