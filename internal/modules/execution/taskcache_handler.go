package execution

import (
	"container/list"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
	"wave-ai.local/wave/internal/platform/observe"
)

// taskResponseCache serves repeated polls of the same task without re-reading
// the heavy JSONB columns. Every write to a task row advances updated_at
// (gorm maintains the column on Updates), so a one-row, small-column probe
// is enough to validate a cached response body: identical updated_at means
// the serialized task cannot have changed. Entries are keyed by task ID and
// evicted LRU; eviction only costs a later cache miss, never correctness.
// Replicas each validate independently, so no cross-process invalidation
// channel exists or is needed.
type taskResponseCache struct {
	mu    sync.Mutex
	capa  int
	lru   *list.List // front = most recent; values are *taskCacheEntry
	byKey map[string]*list.Element
}

type taskCacheEntry struct {
	id        string
	updatedAt time.Time
	body      []byte
}

func newTaskResponseCache(capacity int) *taskResponseCache {
	return &taskResponseCache{capa: capacity, lru: list.New(), byKey: make(map[string]*list.Element)}
}

func (c *taskResponseCache) get(id string, updatedAt time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[id]; ok {
		e := el.Value.(*taskCacheEntry)
		if e.updatedAt.Equal(updatedAt) {
			c.lru.MoveToFront(el)
			return e.body, true
		}
	}
	return nil, false
}

func (c *taskResponseCache) put(id string, updatedAt time.Time, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[id]; ok {
		e := el.Value.(*taskCacheEntry)
		e.updatedAt, e.body = updatedAt, body
		c.lru.MoveToFront(el)
		return
	}
	if c.lru.Len() >= c.capa {
		if oldest := c.lru.Back(); oldest != nil {
			c.lru.Remove(oldest)
			delete(c.byKey, oldest.Value.(*taskCacheEntry).id)
		}
	}
	el := c.lru.PushFront(&taskCacheEntry{id: id, updatedAt: updatedAt, body: body})
	c.byKey[id] = el
}

// ownedUpdatedAt is the validation probe: one small-column read that also
// enforces session ownership, without touching any JSONB payload.
func ownedUpdatedAt(db *gorm.DB, p *auth.Principal, id string) (time.Time, error) {
	from := clause.From{Joins: []clause.Join{{
		Type: clause.InnerJoin, Table: clause.Table{Name: "sessions"},
		ON: clause.Where{Exprs: []clause.Expression{clause.Eq{
			Column: clause.Column{Table: "sessions", Name: "id"},
			Value:  clause.Column{Table: "tasks", Name: "session_id"},
		}}},
	}}}
	owned := clause.And(
		clause.Eq{Column: clause.Column{Table: "sessions", Name: "org_id"}, Value: p.OrgID},
		clause.Eq{Column: clause.Column{Table: "sessions", Name: "owner_id"}, Value: p.PrincipalID},
		clause.Eq{Column: clause.Column{Table: "tasks", Name: "id"}, Value: id},
	)
	var updatedAt time.Time
	e := db.Model(&Task{}).Clauses(from).Select("tasks.updated_at").Where(owned).Take(&updatedAt).Error
	return updatedAt, e
}

// serveTask renders the task response, consulting the cache after the
// updated_at probe and refreshing it on miss or version change.
func (h handler) serveTask(c *gin.Context) {
	p := c.MustGet("principal").(*auth.Principal)
	id := c.Param("id")
	updatedAt, e := ownedUpdatedAt(h.db.WithContext(c.Request.Context()), p, id)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	if body, ok := h.cache.get(id, updatedAt); ok {
		correlateIDs(c, id)
		c.Data(200, "application/json; charset=utf-8", body)
		return
	}
	var t taskView
	from := clause.From{Joins: []clause.Join{{
		Type: clause.InnerJoin, Table: clause.Table{Name: "sessions"},
		ON: clause.Where{Exprs: []clause.Expression{clause.Eq{
			Column: clause.Column{Table: "sessions", Name: "id"},
			Value:  clause.Column{Table: "tasks", Name: "session_id"},
		}}},
	}}}
	owned := clause.And(
		clause.Eq{Column: clause.Column{Table: "sessions", Name: "org_id"}, Value: p.OrgID},
		clause.Eq{Column: clause.Column{Table: "sessions", Name: "owner_id"}, Value: p.PrincipalID},
		clause.Eq{Column: clause.Column{Table: "tasks", Name: "id"}, Value: id},
	)
	if e := h.db.WithContext(c.Request.Context()).Model(&Task{}).Clauses(from).Select(taskViewColumns).Where(owned).Take(&t).Error; e != nil {
		httpx.Error(c, e)
		return
	}
	t.normalize()
	body, e := json.Marshal(t)
	if e != nil {
		httpx.Error(c, e)
		return
	}
	h.cache.put(id, t.UpdatedAt, body)
	correlateTask(c, t.ID, t.RootID, t.SessionID)
	c.Data(200, "application/json; charset=utf-8", body)
}

// correlateIDs sets the request correlation when only the ID is known (cache
// hits skip the full row read).
func correlateIDs(c *gin.Context, id string) {
	s := observe.From(c.Request.Context())
	s.TaskID = id
	c.Request = c.Request.WithContext(observe.With(c.Request.Context(), s))
}
