import { describe, expect, it } from "vitest";
import { figuresReviewContent, reviewContent } from "../../fixtures/content";
import { collectPathDecisions } from "../documents/model";
import { figuresReviewTimeline } from "../../fixtures/documents";
import { buildReviewGuidance } from "./reviewGuidance";

describe("review guidance", () => {
  it("recommends Docling or Gemini when figures are missing and Docling was unset", () => {
    const guidance = buildReviewGuidance(
      figuresReviewContent.content,
      collectPathDecisions(figuresReviewTimeline),
    );
    expect(guidance.missingFigures).toBe(true);
    expect(guidance.doclingUnset).toBe(true);
    expect(guidance.chainExhausted).toBe(true);
    expect(guidance.failedCodes).toEqual(
      expect.arrayContaining(["figures_missing", "extractor_unset", "chain_exhausted"]),
    );
    expect(guidance.recommendedAction).toMatch(/Enable Docling|Start Docling|Gemini/i);
  });

  it("falls back to a generic review action for other quality flags", () => {
    const guidance = buildReviewGuidance(reviewContent.content);
    expect(guidance.missingFigures).toBe(false);
    expect(guidance.recommendedAction).toMatch(/re-upload|pin a stronger extractor/i);
  });
});
