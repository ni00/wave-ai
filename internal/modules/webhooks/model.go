package webhooks

import (
	"time"
	"wave-ai.local/wave/internal/platform/auth"
)

type Subscription struct {
	Organization auth.Organization `gorm:"foreignKey:OrgID" json:"-"`
	Owner        auth.Identity     `gorm:"foreignKey:OwnerID" json:"-"`
	ID           string            `gorm:"primaryKey" json:"id"`
	OrgID        string            `gorm:"type:uuid;index" json:"-"`
	OwnerID      string            `gorm:"type:uuid;index" json:"-"`
	Name         string            `json:"name"`
	URL          string            `json:"url"`
	Events       []string          `gorm:"serializer:json;type:jsonb" json:"events"`
	Paused       bool              `json:"paused"`
	Ciphertext   []byte            `json:"-"`
	Nonce        []byte            `json:"-"`
	CreatedAt    time.Time         `json:"created_at"`
}

func (Subscription) TableName() string { return "webhook_subscriptions" }

type Delivery struct {
	ID             string     `gorm:"primaryKey" json:"id"`
	SubscriptionID string     `gorm:"index;uniqueIndex:webhook_event,priority:1" json:"subscription_id"`
	EventID        string     `gorm:"uniqueIndex:webhook_event,priority:2" json:"event_id"`
	EventType      string     `json:"event_type"`
	Payload        []byte     `json:"-"`
	Status         string     `gorm:"index:webhook_pending,priority:1" json:"status"`
	Attempts       int        `json:"attempts"`
	NextAt         time.Time  `gorm:"index:webhook_pending,priority:2" json:"next_at"`
	LeaseUntil     *time.Time `json:"-"`
	Claim          string     `json:"-"`
	HTTPStatus     int        `json:"http_status,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
}

func (Delivery) TableName() string { return "webhook_deliveries" }

type CreateRequest struct {
	Name   string   `json:"name" binding:"required"`
	URL    string   `json:"url" binding:"required"`
	Events []string `json:"events" binding:"required"`
	Secret string   `json:"secret" binding:"required"`
}
type UpdateRequest struct {
	Paused *bool `json:"paused" binding:"required"`
}
type ListResponse struct {
	Data       []Subscription `json:"data"`
	NextOffset int            `json:"next_offset,omitempty"`
}
type DeliveriesResponse struct {
	Data       []Delivery `json:"data"`
	NextOffset int        `json:"next_offset,omitempty"`
}
