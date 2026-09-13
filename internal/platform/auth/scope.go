package auth

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Owned(db *gorm.DB, p *Principal) *gorm.DB {
	return db.Where(clause.And(clause.Eq{Column: "org_id", Value: p.OrgID}, clause.Eq{Column: "owner_id", Value: p.PrincipalID}))
}
