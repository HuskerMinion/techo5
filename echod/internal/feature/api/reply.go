package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// Actions with an answer: Home Assistant runs an action the device asks for and sends back what it
// returned, matched to the question by a call id. Only Home Assistant answers, and only when "Allow the
// device to perform Home Assistant actions" is on; with it off it raises a repair and says nothing, so a
// question that is not answered in replyWait is given up on.

// replyWait is how long an answer may take. Home Assistant answers once the action is done, and the actions
// asked this way are quick ones; one that does not answer at all is one that was not allowed.
var replyWait = 10 * time.Second

var (
	// errNoHomeAssistant is a question with nobody to ask: no Home Assistant new enough is connected.
	errNoHomeAssistant = errors.New("api: no Home Assistant connected that can answer")

	// errNoAnswer is a question that was sent and never answered, which is what Home Assistant does
	// when the device is not allowed to perform actions.
	errNoAnswer = errors.New(`api: Home Assistant did not answer; is "Allow the device to perform Home Assistant actions" on?`)
)

// replies is the connections a question can go to and the questions waiting for their answers.
type replies struct {
	mu      sync.Mutex
	next    uint32
	waiting map[uint32]func(json.RawMessage, error)

	// conns is each connection by when it was last heard from, so that a question goes to the one
	// Home Assistant that is most recently there.
	conns map[*esphome.Conn]time.Time
}

func newReplies() *replies {
	return &replies{waiting: map[uint32]func(json.RawMessage, error){}, conns: map[*esphome.Conn]time.Time{}}
}

// Handle notes the connection every message comes over and hands an answer to the question it answers.
// It passes everything on, so it can sit anywhere in the chain.
func (r *replies) Handle(_ context.Context, c *esphome.Conn, msg proto.Message) error {
	r.mu.Lock()
	r.conns[c] = time.Now()
	answer, ok := msg.(*api.HomeassistantActionResponse)
	var reply func(json.RawMessage, error)
	if ok {
		reply = r.waiting[answer.GetCallId()]
		delete(r.waiting, answer.GetCallId())
	}
	r.mu.Unlock()

	if reply != nil {
		go reply(unwrapResponse(answer))
	}
	return nil
}

// ConnClosed forgets a connection, which no question can go to any more.
func (r *replies) ConnClosed(c *esphome.Conn) {
	r.mu.Lock()
	delete(r.conns, c)
	r.mu.Unlock()
}

// unwrapResponse is what an answer says: the action's response, or its rendered template, which Home
// Assistant sends inside {"response": ...}, or why it failed.
func unwrapResponse(a *api.HomeassistantActionResponse) (json.RawMessage, error) {
	if !a.GetSuccess() {
		return nil, fmt.Errorf("api: Home Assistant: %s", cmp.Or(a.GetErrorMessage(), "the action failed"))
	}
	var wrapped struct {
		Response json.RawMessage `json:"response"`
	}
	if len(a.GetResponseData()) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(a.GetResponseData(), &wrapped); err != nil {
		return nil, fmt.Errorf("api: reading Home Assistant's answer: %w", err)
	}
	return wrapped.Response, nil
}

// ask sends c to the Home Assistant heard from last among those at least c.MinVersion, and calls c.Reply
// with its answer.
func (r *replies) ask(c component.Call, req *api.HomeassistantActionRequest) {
	r.mu.Lock()
	var to *esphome.Conn
	var last time.Time
	for conn, seen := range r.conns {
		if !conn.StatesSubscribed() || !versionAtLeast(conn.ClientInfo(), c.MinVersion) {
			continue
		}
		if to == nil || seen.After(last) {
			to, last = conn, seen
		}
	}
	if to == nil {
		r.mu.Unlock()
		go c.Reply(nil, errNoHomeAssistant)
		return
	}
	r.next++
	if r.next == 0 {
		// Zero is a call that wants no answer.
		r.next = 1
	}
	id := r.next
	var once sync.Once
	reply := func(resp json.RawMessage, err error) { once.Do(func() { c.Reply(resp, err) }) }
	r.waiting[id] = reply
	r.mu.Unlock()

	numbered(req, id, c.ResponseTemplate)
	if err := to.Send(req); err != nil {
		r.forget(id)
		go reply(nil, fmt.Errorf("api: asking Home Assistant: %w", err))
		return
	}
	slog.Info("home assistant action asked", "service", c.Service, "call", id, "to", to.ClientInfo())
	time.AfterFunc(replyWait, func() {
		if r.forget(id) {
			reply(nil, errNoAnswer)
		}
	})
}

// numbered makes req a question Home Assistant answers, under id. An action that returns nothing refuses
// to be asked for a response: one asked without a template is asked only whether it worked, which Home
// Assistant answers for every call with an id.
func numbered(req *api.HomeassistantActionRequest, id uint32, template string) {
	req.CallId, req.WantsResponse, req.ResponseTemplate = id, template != "", template
}

// forget takes a question off the waiting list, and reports whether it was still on it.
func (r *replies) forget(id uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.waiting[id]
	delete(r.waiting, id)
	return ok
}

// versionAtLeast reports whether a client that introduces itself as info, "Home Assistant 2026.12.0b0"
// say, is at least min, "2026.12". Every client is when min is empty; one that does not say which Home
// Assistant it is is not otherwise.
func versionAtLeast(info, min string) bool {
	if min == "" {
		return true
	}
	have, ok := strings.CutPrefix(info, "Home Assistant ")
	if !ok {
		return false
	}
	h, okH := yearMonth(have)
	m, okM := yearMonth(min)
	return okH && okM && (h[0] > m[0] || h[0] == m[0] && h[1] >= m[1])
}

// yearMonth is the year and month a Home Assistant version starts with: 2026 and 12 for "2026.12.0b0".
func yearMonth(v string) ([2]int, bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return [2]int{}, false
	}
	y, errY := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	return [2]int{y, m}, errY == nil && errM == nil
}
