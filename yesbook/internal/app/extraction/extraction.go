// Package extraction wires the configured extractors into routing chains
// and the pipeline budget, for the worker and yesbook-dev.
package extraction

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/avi-p/yesbook/internal/adapter/docling"
	"github.com/avi-p/yesbook/internal/adapter/documentai"
	"github.com/avi-p/yesbook/internal/adapter/gemini"
	"github.com/avi-p/yesbook/internal/adapter/openai"
	"github.com/avi-p/yesbook/internal/adapter/pdfium"
	"github.com/avi-p/yesbook/internal/adapter/vision"
	"github.com/avi-p/yesbook/internal/document"
	"github.com/avi-p/yesbook/internal/processing"
)

// Config is the process environment that selects extractors and budget limits.
// Empty strings mean the corresponding extractor is disabled or the default applies.
type Config struct {
	DoclingURL     string
	DoclingVersion string
	DoclingAPIKey  string

	DocumentAIProcessor string
	DocumentAILocation  string
	DocumentAIVersion   string
	DocumentAIPageCost  string

	GeminiAPIKey   string
	GeminiProject  string
	GeminiLocation string
	GeminiModel    string
	GeminiPriceIn  string
	GeminiPriceOut string

	OpenAIAPIKey   string
	OpenAIModel    string
	OpenAIPriceIn  string
	OpenAIPriceOut string

	MaxAttempts   int64
	MaxCostMicros int64
	MaxMinutes    int64
}

// LoadConfig reads extractor and budget settings from the environment.
func LoadConfig() (Config, error) {
	return loadConfig(os.Getenv)
}

func loadConfig(getenv func(string) string) (Config, error) {
	def := processing.DefaultConfig()
	attempts, err := envInt(getenv, "YESBOOK_MAX_ATTEMPTS", int64(def.Budget.MaxAttempts))
	if err != nil {
		return Config{}, err
	}
	cost, err := envInt(getenv, "YESBOOK_MAX_COST_MICROS", def.Budget.MaxCostMicros)
	if err != nil {
		return Config{}, err
	}
	minutes, err := envInt(getenv, "YESBOOK_MAX_MINUTES", int64(def.Budget.MaxDuration/time.Minute))
	if err != nil {
		return Config{}, err
	}
	if attempts < 0 || cost < 0 || minutes < 0 {
		return Config{}, fmt.Errorf("pipeline limits must not be negative")
	}
	if attempts > int64(math.MaxInt) {
		return Config{}, fmt.Errorf("YESBOOK_MAX_ATTEMPTS exceeds the platform limit")
	}
	return Config{
		DoclingURL:          getenv("YESBOOK_DOCLING_URL"),
		DoclingVersion:      envOr(getenv, "YESBOOK_DOCLING_VERSION", "unpinned"),
		DoclingAPIKey:       getenv("YESBOOK_DOCLING_API_KEY"),
		DocumentAIProcessor: getenv("YESBOOK_DOCUMENTAI_PROCESSOR"),
		DocumentAILocation:  envOr(getenv, "YESBOOK_DOCUMENTAI_LOCATION", "us"),
		DocumentAIVersion:   getenv("YESBOOK_DOCUMENTAI_VERSION"),
		DocumentAIPageCost:  getenv("YESBOOK_DOCUMENTAI_PAGE_COST_MICROS"),
		GeminiAPIKey:        getenv("YESBOOK_GEMINI_API_KEY"),
		GeminiProject:       getenv("YESBOOK_PROJECT"),
		GeminiLocation:      getenv("YESBOOK_GEMINI_LOCATION"),
		GeminiModel:         envOr(getenv, "YESBOOK_GEMINI_MODEL", gemini.DefaultModel),
		GeminiPriceIn:       getenv("YESBOOK_GEMINI_PRICE_IN"),
		GeminiPriceOut:      getenv("YESBOOK_GEMINI_PRICE_OUT"),
		OpenAIAPIKey:        getenv("YESBOOK_OPENAI_API_KEY"),
		OpenAIModel:         getenv("YESBOOK_OPENAI_MODEL"),
		OpenAIPriceIn:       getenv("YESBOOK_OPENAI_PRICE_IN"),
		OpenAIPriceOut:      getenv("YESBOOK_OPENAI_PRICE_OUT"),
		MaxAttempts:         attempts,
		MaxCostMicros:       cost,
		MaxMinutes:          minutes,
	}, nil
}

func envOr(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(getenv func(string) string, key string, fallback int64) (int64, error) {
	v := getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

// Pipeline returns the processing configuration for these limits.
func (c Config) Pipeline() processing.Config {
	cfg := processing.DefaultConfig()
	cfg.Budget = document.Budget{
		MaxAttempts:   int(c.MaxAttempts),
		MaxCostMicros: c.MaxCostMicros,
		MaxDuration:   time.Duration(c.MaxMinutes) * time.Minute,
	}
	return cfg
}

// PipelineConfig applies YESBOOK_MAX_ATTEMPTS, YESBOOK_MAX_COST_MICROS and
// YESBOOK_MAX_MINUTES on top of the defaults.
func PipelineConfig() (processing.Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return processing.Config{}, err
	}
	return cfg.Pipeline(), nil
}

type clientCloser interface {
	Add(io.Closer)
}

// BuildPipeline wires the configured extractor chains and budget into a pipeline.
// The caller owns pdf and supplies the job, version, attempt and object ports.
func BuildPipeline(ctx context.Context, pdf *pdfium.Engine, closers clientCloser, deps processing.Dependencies) (*processing.Pipeline, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	chains, err := buildChains(ctx, cfg, pdf, closers, deps.Logger)
	if err != nil {
		return nil, err
	}
	deps.Inspector = pdf
	deps.Router = processing.NewRouter(chains)
	return processing.NewPipeline(deps, cfg.Pipeline())
}

// Chains builds the routing chains from the extractors that are configured.
// PDFium is always available; the others are enabled by their environment variables.
func Chains(ctx context.Context, pdf *pdfium.Engine, closers clientCloser, log *slog.Logger) (map[document.ContentKind][]processing.Extractor, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	return buildChains(ctx, cfg, pdf, closers, log)
}

func buildChains(ctx context.Context, cfg Config, pdf *pdfium.Engine, closers clientCloser, log *slog.Logger) (map[document.ContentKind][]processing.Extractor, error) {
	var dl, dai, gem, oai processing.Extractor

	if cfg.DoclingURL != "" {
		dl = docling.New(cfg.DoclingURL, cfg.DoclingVersion, cfg.DoclingAPIKey, nil)
	}
	if cfg.DocumentAIProcessor != "" {
		cost, err := envInt(func(string) string { return cfg.DocumentAIPageCost }, "YESBOOK_DOCUMENTAI_PAGE_COST_MICROS", 0)
		if err != nil {
			return nil, err
		}
		e, err := documentai.New(ctx, cfg.DocumentAILocation, cfg.DocumentAIProcessor, cfg.DocumentAIVersion, cost)
		if err != nil {
			return nil, err
		}
		closers.Add(e)
		dai = e
	}
	if cfg.GeminiAPIKey != "" || (cfg.GeminiProject != "" && cfg.GeminiLocation != "") {
		price, err := configuredPrice("YESBOOK_GEMINI", cfg.GeminiPriceIn, cfg.GeminiPriceOut)
		if err != nil {
			return nil, err
		}
		e, err := gemini.New(ctx, gemini.Connection{
			APIKey: cfg.GeminiAPIKey, Project: cfg.GeminiProject, Location: cfg.GeminiLocation,
		}, cfg.GeminiModel, pdf, price)
		if err != nil {
			return nil, err
		}
		gem = e
	}
	if cfg.OpenAIAPIKey != "" {
		price, err := configuredPrice("YESBOOK_OPENAI", cfg.OpenAIPriceIn, cfg.OpenAIPriceOut)
		if err != nil {
			return nil, err
		}
		e, err := openai.New(cfg.OpenAIAPIKey, cfg.OpenAIModel, pdf, price)
		if err != nil {
			return nil, err
		}
		oai = e
	}

	if cfg.DoclingURL == "" {
		log.WarnContext(ctx, "Docling is not configured; Automatic+images will skip it and escalate to later extractors when present",
			"hint", "set YESBOOK_DOCLING_URL=http://localhost:5001 and start Docling via docker compose / make dev")
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

func configuredPrice(prefix, rawIn, rawOut string) (vision.Pricing, error) {
	in, err := envInt(func(string) string { return rawIn }, prefix+"_PRICE_IN", 0)
	if err != nil {
		return vision.Pricing{}, err
	}
	out, err := envInt(func(string) string { return rawOut }, prefix+"_PRICE_OUT", 0)
	if err != nil {
		return vision.Pricing{}, err
	}
	return vision.Pricing{InputPerMillion: in, OutputPerMillion: out}, nil
}
