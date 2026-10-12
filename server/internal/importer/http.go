package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds one API response; trackers page their lists, so
// nothing honest is anywhere near it.
const maxResponseBytes = 32 << 20

// maxRateLimitWait is the longest an import sits out a rate limit before
// giving up (GitHub's hourly allowance refills within the hour).
const maxRateLimitWait = 65 * time.Minute

// sleep waits, or returns early if the import is cancelled. Tests replace it.
var sleep = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// TokenFunc gives the access token of a connected account, for sources read
// through a connection rather than a pasted token. used is the token a
// request was just refused with, or empty before a first try; given one, the
// function gets a newer token if it can.
type TokenFunc func(ctx context.Context, used string) (string, error)

// apiClient calls a tracker's HTTP API as one account.
type apiClient struct {
	http *http.Client
	// name is the tracker's, for messages: "GitHub".
	name string
	// headers go on every request: authorization, API version.
	headers map[string]string
	// token, when set, supplies the bearer token of each request.
	token TokenFunc
	// progress is told when the client is waiting out a rate limit.
	progress func(string)
}

// call makes one request and decodes the JSON answer into out. It waits out
// rate limits and retries what a busy server refuses; what the account is
// not allowed, or what isn't there, comes back as a UserError.
func (c *apiClient) call(ctx context.Context, method, url string, body, out any) (http.Header, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	renewed := false
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
		if err != nil {
			return nil, userError("That isn't a usable address: " + url)
		}
		bearer := ""
		if c.token != nil {
			if bearer, err = c.token(ctx, ""); err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for name, value := range c.headers {
			req.Header.Set(name, value)
		}
		res, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < 3 {
				if err := sleep(ctx, time.Duration(attempt)*2*time.Second); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("%s could not be reached: %w", c.name, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
		res.Body.Close()

		if wait, limited := rateLimitWait(res, time.Now()); limited {
			if wait > maxRateLimitWait || attempt > 8 {
				return nil, userError(fmt.Sprintf("%s is limiting how fast this account may read. Try again later.", c.name))
			}
			if c.progress != nil {
				c.progress(fmt.Sprintf("Waiting for %s to allow more requests (until %s)", c.name, time.Now().Add(wait).UTC().Format("15:04 UTC")))
			}
			if err := sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		// A token that ran out part way: once, try with a newer one.
		if res.StatusCode == http.StatusUnauthorized && c.token != nil && !renewed {
			renewed = true
			if fresh, err := c.token(ctx, bearer); err == nil && fresh != bearer {
				continue
			}
		}
		switch {
		case res.StatusCode >= 500 && attempt < 4:
			if err := sleep(ctx, time.Duration(attempt)*3*time.Second); err != nil {
				return nil, err
			}
			continue
		case res.StatusCode == http.StatusUnauthorized:
			return nil, userError(fmt.Sprintf("%s refused the credentials. Check the token and try again.", c.name))
		case res.StatusCode == http.StatusForbidden:
			return nil, userError(fmt.Sprintf("%s says this account may not read that. Check what the token is allowed to see.", c.name))
		case res.StatusCode == http.StatusNotFound:
			return nil, userError(fmt.Sprintf("%s has nothing at that address, or this account can't see it.", c.name))
		case res.StatusCode >= 400:
			return nil, fmt.Errorf("%s answered %d: %s", c.name, res.StatusCode, clip(strings.TrimSpace(string(data)), 300))
		}
		if readErr != nil {
			return nil, fmt.Errorf("%s: reading the answer: %w", c.name, readErr)
		}
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				return nil, fmt.Errorf("%s sent something that isn't the expected JSON: %w", c.name, err)
			}
		}
		return res.Header, nil
	}
}

// get is call for the common case.
func (c *apiClient) get(ctx context.Context, url string, out any) (http.Header, error) {
	return c.call(ctx, http.MethodGet, url, nil, out)
}

// open fetches a file with the client's credentials.
func (c *apiClient) open(ctx context.Context, url string) (ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	for name, value := range c.headers {
		if name != "Accept" {
			req.Header.Set(name, value)
		}
	}
	if c.token != nil {
		bearer, err := c.token(ctx, "")
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, "", fmt.Errorf("%s answered %d for a file", c.name, res.StatusCode)
	}
	return res.Body, res.Header.Get("Content-Type"), nil
}

// rateLimitWait reports whether a response is a rate limit, and how long to
// wait before asking again.
func rateLimitWait(res *http.Response, now time.Time) (time.Duration, bool) {
	limited := res.StatusCode == http.StatusTooManyRequests ||
		(res.StatusCode == http.StatusForbidden && (res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("Retry-After") != ""))
	if !limited {
		return 0, false
	}
	if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds >= 0 {
		return time.Duration(seconds+1) * time.Second, true
	}
	// GitHub and Linear say when the allowance refills, as a Unix time (in
	// seconds, or for Linear in milliseconds).
	for _, name := range []string{"X-RateLimit-Reset", "X-RateLimit-Requests-Reset"} {
		if at, err := strconv.ParseInt(res.Header.Get(name), 10, 64); err == nil && at > 0 {
			if at > 1e12 {
				at /= 1000
			}
			if wait := time.Unix(at, 0).Sub(now); wait > 0 {
				return wait + time.Second, true
			}
			return time.Second, true
		}
	}
	return 30 * time.Second, true
}

// nextLink is the "next" page address from a Link header, or "".
func nextLink(h http.Header) string {
	for _, part := range strings.Split(h.Get("Link"), ",") {
		address, params, found := strings.Cut(part, ";")
		if found && strings.Contains(params, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(address), "<>")
		}
	}
	return ""
}
