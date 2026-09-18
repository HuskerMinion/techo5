package hass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Media is one entry of a media browser listing: a station, or a folder of them.
type Media struct {
	Title     string  `json:"title"`
	ID        string  `json:"media_content_id"`
	Kind      string  `json:"media_content_type"`
	CanPlay   bool    `json:"can_play"`
	CanExpand bool    `json:"can_expand"` // a folder
	Thumb     string  `json:"thumbnail"`
	Children  []Media `json:"children"`
}

// wsSession is one authenticated websocket connection to Home Assistant, for one or several
// commands. Home Assistant only offers media browsing and resolving over this API, so a connection
// is opened for a question (or a walk of a folder tree) rather than kept around for the rare use
// these get.
type wsSession struct {
	conn *websocket.Conn
	next int
}

// wsOpen dials and authenticates. The caller closes the connection.
func (c *Client) wsOpen(ctx context.Context) (*wsSession, error) {
	c.mu.Lock()
	acc := c.acc
	c.mu.Unlock()
	if acc.URL == "" || acc.Token == "" {
		return nil, errors.New("hass: no access configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(acc.URL, "http") + "/api/websocket"
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, url, http.Header{})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("hass: websocket: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(dl)
		_ = conn.SetWriteDeadline(dl)
	}
	var hello struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := conn.ReadJSON(&hello); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteJSON(map[string]string{"type": "auth", "access_token": acc.Token}); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.ReadJSON(&hello); err != nil {
		conn.Close()
		return nil, err
	}
	if hello.Type != "auth_ok" {
		conn.Close()
		return nil, fmt.Errorf("hass: websocket auth: %s %s", hello.Type, hello.Message)
	}
	return &wsSession{conn: conn}, nil
}

// call sends one command and returns its raw result, allowing it 30 seconds.
func (s *wsSession) call(cmd map[string]any) (json.RawMessage, error) {
	s.next++
	id := s.next
	cmd["id"] = id
	dl := time.Now().Add(30 * time.Second)
	_ = s.conn.SetReadDeadline(dl)
	_ = s.conn.SetWriteDeadline(dl)
	if err := s.conn.WriteJSON(cmd); err != nil {
		return nil, err
	}
	for {
		var res struct {
			ID      int             `json:"id"`
			Type    string          `json:"type"`
			Success bool            `json:"success"`
			Result  json.RawMessage `json:"result"`
			Error   struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := s.conn.ReadJSON(&res); err != nil {
			return nil, err
		}
		if res.ID != id || res.Type != "result" {
			continue
		}
		if !res.Success {
			return nil, fmt.Errorf("hass: %v: %s %s", cmd["type"], res.Error.Code, res.Error.Message)
		}
		return res.Result, nil
	}
}

// wsCall opens a connection for one command and returns its raw result.
func (c *Client) wsCall(ctx context.Context, cmd map[string]any) (json.RawMessage, error) {
	s, err := c.wsOpen(ctx)
	if err != nil {
		return nil, err
	}
	defer s.conn.Close()
	return s.call(cmd)
}

// BrowseTree lists root and the folders under it, breadth first, over one connection, handing each
// listing to visit, which returns false to stop the walk. At most maxFolders are listed. A folder
// that fails to list is passed over; only the root failing is an error.
func (c *Client) BrowseTree(ctx context.Context, root string, maxFolders int, visit func(Media) bool) error {
	s, err := c.wsOpen(ctx)
	if err != nil {
		return err
	}
	defer s.conn.Close()
	queue := []string{root}
	for n := 0; len(queue) > 0 && n < maxFolders; n++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		id := queue[0]
		queue = queue[1:]
		out, err := s.call(map[string]any{"type": "media_source/browse_media", "media_content_id": id})
		if err != nil {
			if id == root {
				return err
			}
			continue
		}
		var m Media
		if json.Unmarshal(out, &m) != nil {
			continue
		}
		if !visit(m) {
			return nil
		}
		for _, ch := range m.Children {
			if ch.CanExpand {
				queue = append(queue, ch.ID)
			}
		}
	}
	return nil
}

// Browse lists a media source folder, like media-source://radio_browser/local.
func (c *Client) Browse(ctx context.Context, id string) (Media, error) {
	out, err := c.wsCall(ctx, map[string]any{"type": "media_source/browse_media", "media_content_id": id})
	if err != nil {
		return Media{}, err
	}
	var m Media
	return m, json.Unmarshal(out, &m)
}

// Resolved is a media source item resolved to something fetchable: a URL (Home Assistant's own,
// or an absolute one an external source handed back) and its MIME type.
type Resolved struct {
	URL  string `json:"url"`
	MIME string `json:"mime_type"`
}

// ResolveMedia turns a media source id — usually one of Browse's playable children — into a
// fetchable URL. The URL may be relative to Home Assistant or, for some sources, already absolute.
func (c *Client) ResolveMedia(ctx context.Context, id string) (Resolved, error) {
	out, err := c.wsCall(ctx, map[string]any{"type": "media_source/resolve_media", "media_content_id": id})
	if err != nil {
		return Resolved{}, err
	}
	var r Resolved
	return r, json.Unmarshal(out, &r)
}
