/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client is a small client for the OpenFGA HTTP API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// TupleKey is an OpenFGA tuple.
type TupleKey struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// Change is one entry of the OpenFGA changelog.
type Change struct {
	TupleKey  TupleKey  `json:"tuple_key"`
	Operation string    `json:"operation"`
	Timestamp time.Time `json:"timestamp"`
}

const opWrite = "TUPLE_OPERATION_WRITE"

// StoreIDs returns the id of every store by name. Platform Mesh names each
// org's store after the org.
func (c *Client) StoreIDs(ctx context.Context) (map[string]string, error) {
	ids := map[string]string{}
	token := ""
	for {
		var page struct {
			Stores []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"stores"`
			ContinuationToken string `json:"continuation_token"`
		}
		q := url.Values{"page_size": {"100"}}
		if token != "" {
			q.Set("continuation_token", token)
		}
		if err := c.get(ctx, "/stores?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		for _, s := range page.Stores {
			ids[s.Name] = s.ID
		}
		if page.ContinuationToken == "" || len(page.Stores) == 0 {
			return ids, nil
		}
		token = page.ContinuationToken
	}
}

// Changes returns changes on objects of the given type. Without a continuation
// token it starts at start, or at the beginning if start is zero. The returned
// token marks the end of this page; if there was nothing new, it is the token
// that was passed in (or empty).
func (c *Client) Changes(ctx context.Context, storeID, objectType, token string, start time.Time, pageSize int) ([]Change, string, error) {
	q := url.Values{"type": {objectType}, "page_size": {strconv.Itoa(pageSize)}}
	if token != "" {
		q.Set("continuation_token", token)
	} else if !start.IsZero() {
		q.Set("start_time", start.UTC().Format(time.RFC3339))
	}
	var resp struct {
		Changes           []Change `json:"changes"`
		ContinuationToken string   `json:"continuation_token"`
	}
	err := c.get(ctx, "/stores/"+url.PathEscape(storeID)+"/changes?"+q.Encode(), &resp)
	return resp.Changes, resp.ContinuationToken, err
}

// Read returns every tuple on the given object.
func (c *Client) Read(ctx context.Context, storeID, object string) ([]TupleKey, error) {
	var tuples []TupleKey
	token := ""
	for {
		body := map[string]any{"tuple_key": map[string]string{"object": object}, "page_size": 100}
		if token != "" {
			body["continuation_token"] = token
		}
		var page struct {
			Tuples []struct {
				Key TupleKey `json:"key"`
			} `json:"tuples"`
			ContinuationToken string `json:"continuation_token"`
		}
		if err := c.post(ctx, "/stores/"+url.PathEscape(storeID)+"/read", body, &page); err != nil {
			return nil, err
		}
		for _, t := range page.Tuples {
			tuples = append(tuples, t.Key)
		}
		if page.ContinuationToken == "" || len(page.Tuples) == 0 {
			return tuples, nil
		}
		token = page.ContinuationToken
	}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("%s %s: %s: %s", req.Method, req.URL.Path, resp.Status, msg)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
