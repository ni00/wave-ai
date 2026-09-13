package httpx

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
)

// Page is used only on HTTP lists, not queue or execution queries.
func Page(c *gin.Context) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		limit, offset := 100, 0
		var e error
		if s := c.Query("limit"); s != "" {
			limit, e = strconv.Atoi(s)
			if e != nil || limit < 1 || limit > 200 {
				db.AddError(apierr.Invalid("limit must be 1..200"))
				return db
			}
		}
		if s := c.Query("offset"); s != "" {
			offset, e = strconv.Atoi(s)
			if e != nil || offset < 0 {
				db.AddError(apierr.Invalid("offset must be nonnegative"))
				return db
			}
		}
		c.Set("page_limit", limit)
		c.Set("page_offset", offset)
		return db.Order(clause.OrderByColumn{Column: clause.Column{Name: clause.PrimaryKey}}).Offset(offset).Limit(limit + 1)
	}
}
func List[T any](c *gin.Context, rows []T) {
	out := gin.H{"data": rows}
	if n := c.GetInt("page_limit"); n > 0 && len(rows) > n {
		out["data"] = rows[:n]
		out["next_offset"] = c.GetInt("page_offset") + n
	}
	c.JSON(200, out)
}
