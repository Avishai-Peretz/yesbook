// Package gemini extracts handwriting, formulas, diagrams and hard scans with
// Gemini on Vertex AI.
package gemini

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/genai"

	"github.com/avi-p/yesbook/internal/adapter/vision"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

const DefaultModel = "gemini-3.8-flash"

type Extractor struct {
	client   *genai.Client
	model    string
	renderer vision.Renderer
	pricing  vision.Pricing
}

// New uses Application Default Credentials; on Cloud Run that is the
// worker's service account, which needs roles/aiplatform.user.
func New(ctx context.Context, project, location, model string, r vision.Renderer, pricing vision.Pricing) (*Extractor, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: location,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini client: %w", err)
	}
	return &Extractor{client: client, model: model, renderer: r, pricing: pricing}, nil
}

func (e *Extractor) Name() string    { return "gemini" }
func (e *Extractor) Version() string { return e.model }

func (e *Extractor) Extract(ctx context.Context, req processing.ExtractRequest) (document.Extraction, error) {
	pages, err := vision.RenderTarget(ctx, e.renderer, req.PDF, req.Target)
	if err != nil {
		return document.Extraction{}, err
	}
	parts := make([]*genai.Part, 0, len(pages)+1)
	for _, p := range pages {
		parts = append(parts, genai.NewPartFromBytes(p.PNG, "image/png"))
	}
	parts = append(parts, genai.NewPartFromText(vision.Prompt(req.Target)))

	resp, err := e.client.Models.GenerateContent(ctx, e.model,
		[]*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)},
		&genai.GenerateContentConfig{
			SystemInstruction:  genai.NewContentFromText(vision.SystemPrompt, genai.RoleUser),
			Temperature:        genai.Ptr[float32](0),
			ResponseMIMEType:   "application/json",
			ResponseJsonSchema: vision.Schema(),
		})
	if err != nil {
		return document.Extraction{}, fmt.Errorf("gemini generate: %w", err)
	}

	var cost int64
	if u := resp.UsageMetadata; u != nil {
		cost = e.pricing.Cost(int64(u.PromptTokenCount), int64(u.CandidatesTokenCount)+int64(u.ThoughtsTokenCount))
	}
	text := resp.Text()
	if text == "" {
		return document.Extraction{CostMicros: cost}, errors.New("gemini returned no content")
	}
	blocks, err := vision.Parse(text, req.Target)
	if err != nil {
		return document.Extraction{CostMicros: cost}, err
	}
	return document.Extraction{Blocks: blocks, CostMicros: cost}, nil
}
