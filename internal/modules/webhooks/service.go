package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/egress"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/secrets"
	"wave-ai.local/wave/internal/platform/xid"
)

var eventTypes = map[string]bool{"task.created": true, "task.finished": true, "task.unknown": true, "tool.created": true, "budget.exhausted": true, "deployment.run": true}

func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Subscription, error) {
	var s Subscription
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&s).Error
	return s, e
}
func Create(ctx context.Context, db *gorm.DB, box *secrets.Box, p *auth.Principal, in CreateRequest) (Subscription, error) {
	s := Subscription{ID: xid.New("webhook"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: in.Name, URL: in.URL, Events: in.Events}
	u, e := url.Parse(in.URL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return s, apierr.Invalid("webhook URL must be HTTPS without credentials, query or fragment")
	}
	if len(in.Secret) < 32 || len(in.Secret) > 1024 {
		return s, apierr.Invalid("signing secret must contain 32 to 1024 bytes")
	}
	if len(in.Events) == 0 || len(in.Events) > len(eventTypes) {
		return s, apierr.Invalid("invalid event selection")
	}
	for _, event := range in.Events {
		if !eventTypes[event] {
			return s, apierr.Invalid("unsupported webhook event")
		}
	}
	s.Ciphertext, s.Nonce, e = box.Seal([]byte(in.Secret))
	if e != nil {
		return s, e
	}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize subscription admission per identity to keep event fanout bounded.
		var identity auth.Identity
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(clause.Eq{Column: "id", Value: p.PrincipalID}).Take(&identity).Error; e != nil {
			return e
		}
		var count int64
		if e := auth.Owned(tx.Model(&Subscription{}), p).Count(&count).Error; e != nil {
			return e
		}
		if count >= 20 {
			return apierr.Invalid("at most 20 webhook subscriptions")
		}
		return tx.Create(&s).Error
	})
	return s, e
}

// Enqueue participates in the originating state transaction; payloads contain
// identifiers and state only. Receivers fetch authorized details through the API.
func Enqueue(tx *gorm.DB, p *auth.Principal, eventID, typ string, data map[string]any) error {
	if !eventTypes[typ] {
		return nil
	}
	subscriptions := []Subscription{}
	if e := auth.Owned(tx, p).Where(clause.Eq{Column: "paused", Value: false}).Limit(20).Find(&subscriptions).Error; e != nil {
		return e
	}
	payload, e := json.Marshal(map[string]any{"id": eventID, "type": typ, "created_at": time.Now().UTC(), "data": data})
	if e != nil {
		return e
	}
	for _, s := range subscriptions {
		enabled := false
		for _, t := range s.Events {
			if t == typ {
				enabled = true
				break
			}
		}
		if !enabled {
			continue
		}
		d := Delivery{ID: xid.New("delivery"), SubscriptionID: s.ID, EventID: eventID, EventType: typ, Payload: payload, Status: "pending", NextAt: time.Now()}
		if e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&d).Error; e != nil {
			return e
		}
	}
	return nil
}
func Signature(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}
func Run(ctx context.Context, db *gorm.DB, box *secrets.Box) {
	client := egress.GuardedClient(15*time.Second, false)
	// A proxy could resolve the target independently of the guarded dialer.
	client.Transport.(*http.Transport).Proxy = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer client.CloseIdleConnections()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for i := 0; i < 20; i++ {
			worked, e := DeliverOne(ctx, db, box, client)
			if e != nil {
				slog.Error("webhook.delivery_failed", "error_kind", observe.ErrorKind(e))
				break
			}
			if !worked {
				break
			}
		}
	}
}
func DeliverOne(ctx context.Context, db *gorm.DB, box *secrets.Box, client *http.Client) (bool, error) {
	var d Delivery
	claim := xid.New("deliveryclaim")
	e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		due := clause.Or(clause.And(clause.Eq{Column: "status", Value: "pending"}, clause.Lte{Column: "next_at", Value: now}), clause.And(clause.Eq{Column: "status", Value: "sending"}, clause.Lte{Column: "lease_until", Value: now}))
		e := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where(due).Order(clause.OrderByColumn{Column: clause.Column{Name: "next_at"}}).Take(&d).Error
		if e != nil {
			return e
		}
		until := now.Add(time.Minute)
		d.Status = "sending"
		d.Claim = claim
		d.LeaseUntil = &until
		d.Attempts++
		return tx.Save(&d).Error
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var s Subscription
	e = db.WithContext(ctx).Where(clause.Eq{Column: "id", Value: d.SubscriptionID}).Take(&s).Error
	code := 0
	message := ""
	success := false
	if e == nil && s.Paused {
		message = "subscription paused"
	} else if e == nil {
		var secret []byte
		secret, e = box.Open(s.Ciphertext, s.Nonce)
		if e == nil {
			var req *http.Request
			req, e = http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(d.Payload))
			if e == nil {
				stamp := strconv.FormatInt(time.Now().Unix(), 10)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Wave-Event-ID", d.EventID)
				req.Header.Set("Wave-Delivery-ID", d.ID)
				req.Header.Set("Wave-Timestamp", stamp)
				req.Header.Set("Wave-Signature", Signature(secret, stamp, d.Payload))
				var response *http.Response
				response, e = client.Do(req)
				if e == nil {
					code = response.StatusCode
					io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
					response.Body.Close()
					success = code >= 200 && code < 300
					if !success {
						message = "receiver returned non-success status"
					}
				}
			}
		}
	}
	if e != nil {
		message = "delivery failed"
	}
	status := "pending"
	now := time.Now()
	next := now.Add(time.Duration(1<<min(d.Attempts, 10)) * time.Second)
	var delivered *time.Time
	if success {
		status = "delivered"
		delivered = &now
	} else if d.Attempts >= 8 {
		status = "failed"
	}
	if s.Paused && e == nil {
		status = "paused"
		d.Attempts--
	}
	update := map[string]any{"attempts": d.Attempts, "status": status, "http_status": code, "error": message, "next_at": next, "lease_until": nil, "claim": "", "delivered_at": delivered}
	e = db.WithContext(ctx).Model(&Delivery{}).Where(clause.And(clause.Eq{Column: "id", Value: d.ID}, clause.Eq{Column: "claim", Value: claim})).Updates(update).Error
	return true, e
}
