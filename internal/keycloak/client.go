/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package keycloak reads events and users from the Keycloak admin API with a
// service account that only has view-events and view-users.
package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Timeout limits every request to Keycloak, token requests included. Without
// it a Keycloak that accepts a connection and then hangs would block the caller.
const Timeout = 10 * time.Second

// Client talks to the Keycloak admin API with a client of the master realm
// that has a service account.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient takes the URL up to and including Keycloak's relative path, for
// example http://keycloak:8080/keycloak. The token is fetched and renewed as
// needed.
func NewClient(ctx context.Context, baseURL, clientID, clientSecret string) *Client {
	cc := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     baseURL + "/realms/master/protocol/openid-connect/token",
	}
	// The token requests use the client in the context, the API requests the
	// one returned.
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: Timeout})
	hc := cc.Client(ctx)
	hc.Timeout = Timeout
	return &Client{baseURL: baseURL, http: hc}
}

// ReadCredentials reads the client id and secret from the files of the mounted
// Secret.
func ReadCredentials(idFile, secretFile string) (id, secret string, err error) {
	b, err := os.ReadFile(idFile)
	if err != nil {
		return "", "", err
	}
	s, err := os.ReadFile(secretFile)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(b)), strings.TrimSpace(string(s)), nil
}

// Realm is the part of a realm we need. ID changes when a realm is recreated.
type Realm struct {
	ID    string `json:"id"`
	Realm string `json:"realm"`
}

// Event is the part of Keycloak's EventRepresentation we use. Keycloak also
// returns IP address, session id and client; we don't read them.
type Event struct {
	ID      string            `json:"id"`
	Time    int64             `json:"time"`
	Type    string            `json:"type"`
	UserID  string            `json:"userId"`
	Details map[string]string `json:"details"`
}

// User is the part of Keycloak's UserRepresentation we use.
type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

func (c *Client) Realms(ctx context.Context) ([]Realm, error) {
	var realms []Realm
	err := c.get(ctx, "/admin/realms", &realms)
	return realms, err
}

// Events returns stored events of one type, oldest first, starting at
// fromMillis (inclusive).
func (c *Client) Events(ctx context.Context, realm, typ string, fromMillis int64, limit int) ([]Event, error) {
	q := url.Values{
		"type":      {typ},
		"dateFrom":  {strconv.FormatInt(fromMillis, 10)},
		"direction": {"asc"},
		"max":       {strconv.Itoa(limit)},
	}
	var events []Event
	if err := c.get(ctx, "/admin/realms/"+url.PathEscape(realm)+"/events?"+q.Encode(), &events); err != nil {
		return nil, err
	}
	// direction=asc promises this order and the cursor relies on it.
	sort.SliceStable(events, func(i, j int) bool { return events[i].Time < events[j].Time })
	return events, nil
}

// User reads a user by Keycloak id. found is false if there is no such user.
func (c *Client) User(ctx context.Context, realm, id string) (User, bool, error) {
	var u User
	err := c.get(ctx, "/admin/realms/"+url.PathEscape(realm)+"/users/"+url.PathEscape(id), &u)
	if isNotFound(err) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

// UserByUsername reads a user by exact username. found is false if there is
// none.
func (c *Client) UserByUsername(ctx context.Context, realm, username string) (User, bool, error) {
	q := url.Values{"username": {username}, "exact": {"true"}}
	var users []User
	err := c.get(ctx, "/admin/realms/"+url.PathEscape(realm)+"/users?"+q.Encode(), &users)
	if isNotFound(err) || (err == nil && len(users) == 0) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return users[0], true, nil
}

// statusError is a non-2xx answer.
type statusError struct {
	path   string
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("GET %s: %d: %s", e.path, e.status, e.body)
}

func isNotFound(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == http.StatusNotFound
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	// Errors name the path only. The query can hold a user's email, and errors
	// end up in logs.
	path, _, _ = strings.Cut(path, "?")
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return &statusError{path: path, status: resp.StatusCode, body: string(msg)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
