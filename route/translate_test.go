package route

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/metatube-community/metatube-sdk-go/route/auth"
	"github.com/metatube-community/metatube-sdk-go/translate"
)

type fixtureTranslator struct {}
func (*fixtureTranslator) Translate(q, from, to string) (string, error) {
	return from + ":" + to + ":" + q, nil
}

func TestPostTranslationAcceptsJSONAndNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	translate.Register(&fixtureTranslator{})
	r := gin.New()
	r.POST("/v1/translate", cacheNoStore(), postTranslate())
	payload := []byte(`{"engine":"fixtureTranslator","q":"hello","to":"zh-CN"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/translate", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
	if !strings.Contains(strings.ToLower(w.Header().Get("Cache-Control")), "no-store") {
		t.Fatalf("response is cacheable: %v", w.Header())
	}
	var result struct {Data struct {Text string `json:"translated_text"`} `json:"data"`}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil { t.Fatal(err) }
	if result.Data.Text != "auto:zh-CN:hello" {t.Fatalf("text=%q",result.Data.Text)}
}

func TestTranslationAuthRejectsMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/translate", authentication(auth.Token("expected")), cacheNoStore(), getTranslate())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/translate?q=secret&to=zh-CN&engine=OpenAI", nil))
	if w.Code != http.StatusUnauthorized { t.Fatalf("want 401 got %d", w.Code) }
}

func TestLoggerDoesNotWriteSensitiveQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logOutput bytes.Buffer
	previous := gin.DefaultWriter
	gin.DefaultWriter = &logOutput
	defer func(){gin.DefaultWriter=previous}()
	r := gin.New()
	r.Use(logger())
	r.GET("/v1/translate", func(c *gin.Context){c.Status(http.StatusNoContent)})
	w:=httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/translate?openai-api-key=highly-sensitive",nil))
	if strings.Contains(logOutput.String(), "highly-sensitive") {t.Fatal("query token leaked in access log")}
}
