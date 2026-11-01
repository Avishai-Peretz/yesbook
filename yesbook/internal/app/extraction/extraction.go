// Package extraction wires the configured extractors into routing chains
// and the pipeline budget, for the worker and yesbook-dev.
package extraction

import (
	"context"
	"log/slog"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/docling"
	"github.com/avi-p/yesbook/internal/adapter/documentai"
	"github.com/avi-p/yesbook/internal/adapter/gemini"
	"github.com/avi-p/yesbook/internal/adapter/openai"
	"github.com/avi-p/yesbook/internal/adapter/pdfium"
	"github.com/avi-p/yesbook/internal/adapter/vision"
	"github.com/avi-p/yesbook/internal/app"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/platform"
	"github.com/avi-p/yesbook/internal/processing"
)

// Chains builds the routing chains from the extractors that are
// configured. PDFium is always available; the others are enabled by their
// environment variables:
//
//	YESBOOK_DOCLING_URL, YESBOOK_DOCLING_VERSION, YESBOOK_DOCLING_API_KEY
//	YESBOOK_DOCUMENTAI_PROCESSOR, YESBOOK_DOCUMENTAI_LOCATION, YESBOOK_DOCUMENTAI_VERSION, YESBOOK_DOCUMENTAI_PAGE_COST_MICROS
//	YESBOOK_PROJECT, YESBOOK_GEMINI_LOCATION, YESBOOK_GEMINI_MODEL, YESBOOK_GEMINI_PRICE_IN, YESBOOK_GEMINI_PRICE_OUT
//	YESBOOK_OPENAI_API_KEY, YESBOOK_OPENAI_MODEL, YESBOOK_OPENAI_PRICE_IN, YESBOOK_OPENAI_PRICE_OUT
//
// Prices are micro-dollars per million tokens.
func Chains(ctx context.Context, pdf *pdfium.Engine, closers *app.Closers, log *slog.Logger) (map[document.ContentKind][]processing.Extractor, error) {
	var dl, dai, gem, oai processing.Extractor

	if url := platform.Env("YESBOOK_DOCLING_URL", ""); url != "" {
		dl = docling.New(url, platform.Env("YESBOOK_DOCLING_VERSION", "unpinned"), platform.Env("YESBOOK_DOCLING_API_KEY", ""), nil)
	}
	if proc := platform.Env("YESBOOK_DOCUMENTAI_PROCESSOR", ""); proc != "" {
		cost, err := platform.EnvInt64("YESBOOK_DOCUMENTAI_PAGE_COST_MICROS", 0)
		if err != nil {
			return nil, err
		}
		e, err := documentai.New(ctx, platform.Env("YESBOOK_DOCUMENTAI_LOCATION", "us"), proc, platform.Env("YESBOOK_DOCUMENTAI_VERSION", ""), cost)
		if err != nil {
			return nil, err
		}
		closers.Add(e)
		dai = e
	}
	if project, loc := platform.Env("YESBOOK_PROJECT", ""), platform.Env("YESBOOK_GEMINI_LOCATION", ""); project != "" && loc != "" {
		price, err := pricing("YESBOOK_GEMINI")
		if err != nil {
			return nil, err
		}
		e, err := gemini.New(ctx, project, loc, platform.Env("YESBOOK_GEMINI_MODEL", gemini.DefaultModel), pdf, price)
		if err != nil {
			return nil, err
		}
		gem = e
	}
	if key := platform.Env("YESBOOK_OPENAI_API_KEY", ""); key != "" {
		price, err := pricing("YESBOOK_OPENAI")
		if err != nil {
			return nil, err
		}
		e, err := openai.New(key, platform.Env("YESBOOK_OPENAI_MODEL", ""), pdf, price)
		if err != nil {
			return nil, err
		}
		oai = e
	}

	chains := map[document.ContentKind][]processing.Extractor{
		document.ContentText:        chain(pdf, dl, gem, oai),
		document.ContentScan:        chain(dl, dai, gem, oai),
		document.ContentTable:       chain(dl, dai, gem),
		document.ContentHandwriting: chain(gem, oai, dai),
		document.ContentFormula:     chain(gem, oai, dai),
		document.ContentDiagram:     chain(gem, oai, dai),
	}
	for kind, c := range chains {
		names := make([]string, len(c))
		for i, e := range c {
			names[i] = e.Name() + "@" + e.Version()
		}
		log.InfoContext(ctx, "extractor chain", "content", kind, "extractors", names)
	}
	return chains, nil
}

func chain(extractors ...processing.Extractor) []processing.Extractor {
	out := make([]processing.Extractor, 0, len(extractors))
	for _, e := range extractors {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

func pricing(prefix string) (vision.Pricing, error) {
	in, err := platform.EnvInt64(prefix+"_PRICE_IN", 0)
	if err != nil {
		return vision.Pricing{}, err
	}
	out, err := platform.EnvInt64(prefix+"_PRICE_OUT", 0)
	if err != nil {
		return vision.Pricing{}, err
	}
	return vision.Pricing{InputPerMillion: in, OutputPerMillion: out}, nil
}

// PipelineConfig applies YESBOOK_MAX_ATTEMPTS, YESBOOK_MAX_COST_MICROS and
// YESBOOK_MAX_MINUTES on top of the defaults.
func PipelineConfig() (processing.Config, error) {
	cfg := processing.DefaultConfig()
	attempts, err := platform.EnvInt64("YESBOOK_MAX_ATTEMPTS", int64(cfg.Budget.MaxAttempts))
	if err != nil {
		return cfg, err
	}
	cost, err := platform.EnvInt64("YESBOOK_MAX_COST_MICROS", cfg.Budget.MaxCostMicros)
	if err != nil {
		return cfg, err
	}
	minutes, err := platform.EnvInt64("YESBOOK_MAX_MINUTES", int64(cfg.Budget.MaxDuration/time.Minute))
	if err != nil {
		return cfg, err
	}
	cfg.Budget = document.Budget{MaxAttempts: int(attempts), MaxCostMicros: cost, MaxDuration: time.Duration(minutes) * time.Minute}
	return cfg, nil
}
