package wave

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

type Event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data string `json:"data"`
}
type StreamOptions struct {
	After         string
	LastEventID   string
	TaskID        string
	MaxReconnects int
}

// Events delivers complete SSE frames before the connection closes. Returning
// an error from visit stops immediately; cancellation is controlled by ctx.
func (c *Client) Events(ctx context.Context, session string, o StreamOptions, visit func(Event) error) error {
	if o.After != "" && o.LastEventID != "" {
		return errors.New("provide after or LastEventID, not both")
	}
	cursor := o.LastEventID
	op := operationRegistry["executionStreamEvents"]
	for attempt := 0; ; attempt++ {
		opts := Options{Path: map[string]string{"id": session}, Query: make(url.Values), Headers: make(http.Header)}
		if cursor != "" {
			opts.Headers.Set("Last-Event-ID", cursor)
		} else if o.After != "" {
			opts.Query.Set("after", o.After)
		}
		opts.Headers.Set("Accept", "text/event-stream")
		req, e := c.request(ctx, op, opts, nil, "")
		if e != nil {
			return e
		}
		r, e := c.httpClient().Do(req)
		if e == nil {
			if r.StatusCode != 200 {
				e = decodeError(r)
				r.Body.Close()
				if !retryStatus(r.StatusCode) {
					return e
				}
			} else {
				typ, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if typ != "text/event-stream" {
					r.Body.Close()
					return errors.New("expected text/event-stream")
				}
				var callbackErr error
				e = readSSE(r.Body, func(event Event) error {
					// Remember IDs even on another task's event, so reconnect never replays it.
					if o.TaskID != "" {
						var data struct {
							TaskID string `json:"task_id"`
						}
						if err := json.Unmarshal([]byte(event.Data), &data); err != nil {
							return err
						}
						if data.TaskID != o.TaskID {
							cursor = event.ID
							return nil
						}
					}
					callbackErr = visit(event)
					if callbackErr == nil {
						cursor = event.ID
					}
					return callbackErr
				})
				r.Body.Close()
				if callbackErr != nil {
					return callbackErr
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= o.MaxReconnects {
			return e
		}
		if e = pause(ctx, retryDelay(nil, attempt)); e != nil {
			return e
		}
	}
}

// splitSSELines handles CRLF, LF and CR, including CRLF across network reads.
func splitSSELines(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' {
			return i + 1, data[:i], nil
		}
		if b == '\r' {
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			n := i + 1
			if n < len(data) && data[n] == '\n' {
				n++
			}
			return n, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
func readSSE(reader io.Reader, visit func(Event) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Split(splitSSELines)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	id, kind := "", ""
	data := []string{}
	size := 0
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" {
			if len(data) > 0 {
				eventType := kind
				if eventType == "" {
					eventType = "message"
				}
				if e := visit(Event{id, eventType, strings.Join(data, "\n")}); e != nil {
					return e
				}
			}
			kind = ""
			data = nil
			size = 0
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			if !strings.ContainsRune(value, 0) {
				id = value
			}
		case "event":
			kind = value
		case "data":
			size += len(value)
			if size > 1<<20 {
				return errors.New("SSE event exceeds 1 MiB")
			}
			data = append(data, value)
		}
	}
	return scanner.Err()
}

type DownloadResult struct {
	StatusCode int         `json:"status"`
	Header     http.Header `json:"headers"`
	Bytes      int64       `json:"bytes"`
}

// Download copies successful bytes only. 304 leaves dst untouched. Caller owns
// dst and decides whether a 206 range should be appended or stored separately.
func (c *Client) Download(ctx context.Context, operation, id string, headers http.Header, dst io.Writer) (*DownloadResult, error) {
	op, ok := operationRegistry[operation]
	if !ok || op.Transport != "download" {
		return nil, fmt.Errorf("not a download operation: %s", operation)
	}
	req, e := c.request(ctx, op, Options{Path: map[string]string{"id": id}, Headers: headers}, nil, "")
	if e != nil {
		return nil, e
	}
	r, e := c.httpClient().Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	out := &DownloadResult{StatusCode: r.StatusCode, Header: r.Header}
	if r.StatusCode == 304 {
		return out, nil
	}
	if r.StatusCode != 200 && r.StatusCode != 206 {
		return nil, decodeError(r)
	}
	typ, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if typ == "application/json" {
		return nil, errors.New("refusing JSON in a binary download")
	}
	out.Bytes, e = io.Copy(dst, r.Body)
	return out, e
}

// Upload streams multipart data without buffering the whole file. No retries
// are attempted: streams and non-idempotent uploads cannot be replayed safely.
func (c *Client) Upload(ctx context.Context, operation, filename string, source io.Reader, fields map[string]string) (*Response, error) {
	op, ok := operationRegistry[operation]
	if !ok || op.Transport != "upload" {
		return nil, fmt.Errorf("not an upload operation: %s", operation)
	}
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	req, e := c.request(ctx, op, Options{}, reader, form.FormDataContentType())
	if e != nil {
		reader.Close()
		writer.Close()
		return nil, e
	}
	go func() {
		for name, value := range fields {
			if e := form.WriteField(name, value); e != nil {
				writer.CloseWithError(e)
				return
			}
		}
		part, e := form.CreateFormFile("file", filename)
		if e == nil {
			_, e = io.Copy(part, source)
		}
		if e == nil {
			e = form.Close()
		}
		writer.CloseWithError(e)
	}()
	defer reader.Close()
	r, e := c.httpClient().Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, decodeError(r)
	}
	data, e := io.ReadAll(r.Body)
	if e != nil {
		return nil, e
	}
	if !json.Valid(data) {
		return nil, errors.New("expected JSON upload response")
	}
	return &Response{r.StatusCode, r.Header, data}, nil
}
