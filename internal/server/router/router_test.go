package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func resetRegistry() {
	registeredRouters = nil
}

func okHandler(c *gin.Context) {
	c.Status(http.StatusOK)
}

// 回归：未知/非规范 HTTP 方法必须报错，而不是静默注册为 GET。
func TestRegisterAllRejectsUnknownMethod(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	gin.SetMode(gin.TestMode)

	NewGroupRouter("/api/test").AddRoute(NewRoute("/thing", "post").Handle(okHandler)) // 小写，非法

	err := RegisterAll(gin.New())
	if err == nil {
		t.Fatalf("lowercase method must be rejected, got nil error")
	}
}

// 缺少 handler 的路由必须报错。
func TestRegisterAllRejectsRouteWithoutHandler(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	gin.SetMode(gin.TestMode)

	NewGroupRouter("/api/test").AddRoute(NewRoute("/thing", http.MethodGet))

	if err := RegisterAll(gin.New()); err == nil {
		t.Fatalf("route without handler must be rejected, got nil error")
	}
}

// 正常注册路径冒烟：标准方法注册成功且可路由。
func TestRegisterAllStandardMethods(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	gin.SetMode(gin.TestMode)

	NewGroupRouter("/api/test").
		AddRoute(NewRoute("/get", http.MethodGet).Handle(okHandler)).
		AddRoute(NewRoute("/post", http.MethodPost).Handle(okHandler)).
		AddRoute(NewRoute("/put", http.MethodPut).Handle(okHandler)).
		AddRoute(NewRoute("/delete", http.MethodDelete).Handle(okHandler)).
		AddRoute(NewRoute("/patch", http.MethodPatch).Handle(okHandler))

	engine := gin.New()
	if err := RegisterAll(engine); err != nil {
		t.Fatalf("RegisterAll failed: %v", err)
	}

	for method, path := range map[string]string{
		http.MethodGet:    "/api/test/get",
		http.MethodPost:   "/api/test/post",
		http.MethodPut:    "/api/test/put",
		http.MethodDelete: "/api/test/delete",
		http.MethodPatch:  "/api/test/patch",
	} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", method, path, w.Code)
		}
	}
}
