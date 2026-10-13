package route

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/schema"

	"github.com/metatube-community/metatube-sdk-go/translate"
	_ "github.com/metatube-community/metatube-sdk-go/translate/baidu"
	_ "github.com/metatube-community/metatube-sdk-go/translate/deepl"
	_ "github.com/metatube-community/metatube-sdk-go/translate/google"
	_ "github.com/metatube-community/metatube-sdk-go/translate/googlefree"
	_ "github.com/metatube-community/metatube-sdk-go/translate/openai"
)

type translateQuery struct {
	Q      string `form:"q" json:"q" binding:"required"`
	From   string `form:"from" json:"from"`
	To     string `form:"to" json:"to" binding:"required"`
	Engine string `form:"engine" json:"engine" binding:"required"`
}

type translateResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"translated_text"`
}

// getTranslate keeps the legacy GET API for compatibility. Use POST to avoid
// putting API keys in URLs, where reverse proxies or browsers may record them.
func getTranslate() gin.HandlerFunc {
	decoder := schema.NewDecoder()
	decoder.SetAliasTag("json")
	decoder.IgnoreUnknownKeys(true)

	return func(c *gin.Context) {
		query := &translateQuery{From: "auto"}
		if err := c.ShouldBindQuery(query); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		decode := func(v any) error {
			return decoder.Decode(v, c.Request.URL.Query())
		}
		serveTranslation(c, query, decode)
	}
}

// postTranslate accepts one flat JSON object containing q/from/to/engine
// alongside existing translator-specific configuration fields.
func postTranslate() gin.HandlerFunc {
	const maxRequestBytes int64 = 64 << 10
	return func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxRequestBytes+1))
		if err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		if int64(len(body)) > maxRequestBytes {
			abortWithStatusMessage(c, http.StatusRequestEntityTooLarge, "translation request too large")
			return
		}
		var query translateQuery
		query.From = "auto"
		if err := json.Unmarshal(body, &query); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		if query.Q == "" || query.To == "" || query.Engine == "" {
			abortWithStatusMessage(c, http.StatusBadRequest, errors.New("q, to, and engine are required"))
			return
		}
		serveTranslation(c, &query, func(v any) error {
			return json.Unmarshal(body, v)
		})
	}
}

func serveTranslation(c *gin.Context, query *translateQuery, decode func(any) error) {
	translator := translate.New(query.Engine, decode)
	var (
		result string
		err    error
	)
	if aware, ok := translator.(translate.ContextTranslator); ok {
		result, err = aware.TranslateContext(c.Request.Context(), query.Q, query.From, query.To)
	} else {
		result, err = translator.Translate(query.Q, query.From, query.To)
	}
	if err != nil {
		abortWithError(c, err)
		return
	}
	c.JSON(http.StatusOK, &responseMessage{Data: &translateResponse{
		From: query.From,
		To:   query.To,
		Text: result,
	}})
}
