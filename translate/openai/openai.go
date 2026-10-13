package openai

import "github.com/metatube-community/metatube-sdk-go/translate"

var (
	_ translate.Translator        = (*OpenAI)(nil)
	_ translate.ContextTranslator = (*OpenAI)(nil)
)

func init() {
	translate.Register(&OpenAI{})
}
