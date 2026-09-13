package auth

import "time"

// Models returns the identity tables for the schema initialization command.
func Models() []any { return []any{&Organization{}, &Identity{}, &APIKey{}} }

type Organization struct {
	ID        string `gorm:"type:uuid;primaryKey"`
	Name      string `gorm:"not null;uniqueIndex"`
	CreatedAt time.Time
}

type Identity struct {
	ID           string       `gorm:"type:uuid;primaryKey"`
	OrgID        string       `gorm:"type:uuid;not null;uniqueIndex:identity_name,priority:1"`
	DisplayName  string       `gorm:"not null;uniqueIndex:identity_name,priority:2"`
	Organization Organization `gorm:"foreignKey:OrgID"`
	CreatedAt    time.Time
}

func (Identity) TableName() string { return "principals" }

type APIKey struct {
	ID           string `gorm:"type:uuid;primaryKey"`
	OrgID        string `gorm:"type:uuid;not null"`
	PrincipalID  string `gorm:"type:uuid;not null;index"`
	KeyHash      string `gorm:"not null;uniqueIndex" json:"-"`
	KeyPrefix    string `gorm:"not null"`
	Label        string `gorm:"not null"`
	Scope        string `gorm:"not null"`
	RevokedAt    *time.Time
	LastUsedAt   *time.Time
	CreatedAt    time.Time
	Organization Organization `gorm:"foreignKey:OrgID" json:"-"`
	Identity     Identity     `gorm:"foreignKey:PrincipalID" json:"-"`
}
