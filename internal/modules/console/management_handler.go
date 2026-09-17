package console

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/skills"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/httpx"
)

func (h handler) resource(c *gin.Context) {
	ctx, cancel := contextTimeout(c)
	defer cancel()
	p := c.MustGet("principal").(*auth.Principal)
	var out Record
	var err error
	switch c.Param("kind") {
	case "skills":
		var s skills.Skill
		var items map[string][]byte
		s, items, err = skills.Load(ctx, h.db, h.blobs, p, c.Param("id"))
		if err == nil {
			out = skillRecord(s)
			raw := items["SKILL.md"]
			truncated := len(raw) > 128<<10
			if truncated {
				raw = raw[:128<<10]
			}
			out.Meta["content"] = string(raw)
			out.Meta["truncated"] = truncated
			names := make([]string, 0, len(items))
			for name := range items {
				names = append(names, name)
			}
			sort.Strings(names)
			out.Meta["files"] = names
		}
	case "deployments":
		var d deployments.Deployment
		err = auth.Owned(h.db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: c.Param("id")}).Take(&d).Error
		if err == nil {
			out = deploymentRecord(d)
			out.Meta["input"] = d.Input
		}
	case "sandboxes":
		var list List
		list, err = browseSandboxes(ctx, h.db, p, Query{SessionID: c.Param("id"), Limit: 1})
		if err == nil {
			if len(list.Data) == 0 {
				err = gorm.ErrRecordNotFound
			} else {
				out = list.Data[0]
			}
		}
	default:
		err = apierr.Invalid("unsupported resource detail")
	}
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, out)
}
