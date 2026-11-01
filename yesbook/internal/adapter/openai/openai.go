// Package openai extracts hard pages with an OpenAI vision model, as the
// last escalation step after Gemini.
package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/avi-p/yesbook/internal/adapter/vision"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

type Extractor struct {
	client   sdk.Client
	model    string
	renderer vision.Renderer
	pricing  vision.Pricing
}

// New takes the API key from Secret Manager (exposed to the worker as an
// environment variable) and a model the account has access to.
func New(apiKey, model string, r vision.Renderer, pricing vision.Pricing) (*Extractor, error) {
	if apiKey == "" || model == "" {
		return nil, errors.New("openai: api key and model are required")
	}
	return &Extractor{client: sdk.NewClient(option.WithAPIKey(apiKey)), model: model, renderer: r, pricing: pricing}, nil
}

func (e *Extractor) Name() string    { return "openai" }
func (e *Extractor) Version() string { return e.model }

func (e *Extractor) Extract(ctx context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	pages, err := vision.RenderTarget(ctx, e.renderer, req.PDF, req.Target)
	if err != nil {
		return document.Extraction{}, err
	}
	parts := make([]sdk.ChatCompletionContentPartUnionParam, 0, len(pages)+1)
	for _, p := range pages {
		parts = append(parts, sdk.ImageContentPart(sdk.ChatCompletionContentPartImageImageURLParam{
			URL:    "data:image/png;base64," + base64.StdEncoding.EncodeToString(p.PNG),
			Detail: "high",
		}))
	}
	parts = append(parts, sdk.TextContentPart(vision.Prompt(req.Target)))

	resp, err := e.client.Chat.Completions.New(ctx, sdk.ChatCompletionNewParams{
		Model: e.model,
		Messages: []sdk.ChatCompletionMessageParamUnion{
			sdk.SystemMessage(vision.SystemPrompt),
			sdk.UserMessage(parts),
		},
		ResponseFormat: sdk.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "page_transcription",
					Strict: sdk.Bool(true),
					Schema: vision.Schema(),
				},
			},
		},
	})
	if err != nil {
		return document.Extraction{}, fmt.Errorf("openai chat completion: %w", err)
	}
	cost := e.pricing.Cost(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	if len(resp.Choices) == 0 {
		return document.Extraction{CostMicros: cost}, errors.New("openai returned no choices")
	}
	if refusal := resp.Choices[0].Message.Refusal; refusal != "" {
		return document.Extraction{CostMicros: cost}, fmt.Errorf("openai refused: %s", refusal)
	}
	if resp.Choices[0].Message.Content == "" {
		return document.Extraction{CostMicros: cost}, errors.New("openai returned no content")
	}
	blocks, err := vision.Parse(resp.Choices[0].Message.Content, req.Target)
	if err != nil {
		return document.Extraction{CostMicros: cost}, err
	}
	return document.Extraction{Blocks: blocks, CostMicros: cost}, nil
}
