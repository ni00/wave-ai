package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidatePathParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/v1", ValidatePathParams())
	v.GET("/things/:id", func(c *gin.Context) {
		c.String(http.StatusOK, "id=%s", c.Param("id"))
	})

	cases := []struct {
		target string
		want   int
	}{
		{"/v1/things/abc", http.StatusOK},
		{"/v1/things/agent_01ABC", http.StatusOK},
		{"/v1/things/a%00b", http.StatusBadRequest},
		{"/v1/things/%ff%fe", http.StatusBadRequest}, // invalid UTF-8
		{"/v1/things/a%09b", http.StatusBadRequest},  // control char
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if w.Code != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.target, w.Code, tc.want)
		}
	}
}
